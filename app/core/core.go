// Package core wires configuration and infrastructure into the application
// services, without HTTP. The API (package app) and the worker both build on
// it; keeping HTTP out means cmd/worker does not link the router, handlers,
// Guard's HTTP layer or the docs console.
package core

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/bakhod1r/errorx"
	"github.com/bakhod1r/guard"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/config"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/guardauth"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/importer"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/mail"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/markdown"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/postgres"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/queue"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/redis"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/secretbox"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/storage"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/svgsafe"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/webhooks"
)

type Services struct {
	DB      *pgxpool.Pool
	RDB     *goredis.Client
	Guard   *guard.Guard
	Storage *storage.MinIO
	// Uploads is the ledger behind the per-user upload quota.
	Uploads application.UploadLedger
	// UploadGrace is how long an unused upload is kept (0: never removed).
	UploadGrace time.Duration
	Jobs        *queue.Client
	Mailer      application.Mailer
	// Outbox holds sent mail when SMTP is not configured (dev, tests).
	Outbox *mail.Memory

	Blog      *application.Blog
	Accounts  *application.Accounts
	Diagrams  *application.Diagrams
	Analytics *application.Analytics // nil unless cfg.Analytics
	SiteStats *application.SiteStats

	close []func()
}

// Close releases connections in reverse order of opening.
func (s *Services) Close() {
	for i := len(s.close) - 1; i >= 0; i-- {
		s.close[i]()
	}
}

// Handlers is what the background worker needs.
func (s *Services) Handlers() queue.Handlers {
	h := queue.Handlers{Blog: s.Blog, Accounts: s.Accounts, Mailer: s.Mailer, Storage: s.Storage, Analytics: s.Analytics}
	if s.Diagrams != nil {
		h.Hooks = s.Diagrams.Hooks
	}
	if ledger, ok := s.Uploads.(application.UnusedUploads); ok && s.Storage != nil && s.UploadGrace > 0 {
		h.Uploads = &application.UploadSweeper{Ledger: ledger, Storage: s.Storage, Grace: s.UploadGrace, Batch: 50}
	}
	return h
}

func New(ctx context.Context, cfg config.Config) (_ *Services, err error) {
	// s is local, not the named result: "return nil, err" must not hide it
	// from the cleanup below.
	s := &Services{}
	if err := errorx.ValidateRegistryErr(); err != nil {
		return nil, fmt.Errorf("error codes: %w", err)
	}
	defer func() {
		if err != nil {
			s.Close()
		}
	}()

	db, err := postgres.Connect(ctx, cfg.DatabaseURL, postgres.Options{
		MaxConns:         cfg.DBMaxConns,
		MinConns:         cfg.DBMinConns,
		MaxConnLifetime:  cfg.DBMaxConnLifetime,
		StatementTimeout: cfg.DBStatementTimeout,
		Migrate:          cfg.MigrateOnStart,
	})
	if err != nil {
		return nil, err
	}
	s.DB = db
	s.close = append(s.close, db.Close)

	rdb, err := redis.Connect(ctx, cfg.RedisURL)
	if err != nil {
		return nil, err
	}
	s.RDB = rdb
	s.close = append(s.close, func() { rdb.Close() })

	g, err := guardauth.Open(ctx, db, rdb, guardauth.Options{
		HashMemoryKiB: cfg.PasswordHashMemoryKiB,
		HashTime:      cfg.PasswordHashTime,
		HashThreads:   cfg.PasswordHashThreads,
		AuditEmailKey: cfg.AuditEmailKey,
		SessionIdle:   cfg.SessionIdleTimeout, SessionAbsolute: cfg.SessionAbsoluteTimeout,
		// Guard schema + access seed run here only with MIGRATE_ON_START;
		// otherwise cmd/migrate owns them.
		Migrate:     cfg.MigrateOnStart,
		AccessRules: guardauth.NewAccessRules(cfg.ModeratorIPs, cfg.ModeratorHours),
	})
	if err != nil {
		return nil, err
	}
	s.Guard = g
	s.close = append(s.close, func() { g.Close(context.Background()) })

	st, err := storage.NewMinIO(ctx, cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket, cfg.S3UseSSL)
	if err != nil {
		return nil, fmt.Errorf("minio: %w", err)
	}
	s.Storage = st
	s.Uploads = postgres.NewUploadRepo(db)
	s.UploadGrace = time.Duration(max(cfg.UploadUnusedDays, 0)) * 24 * time.Hour

	if cfg.SMTPAddr != "" {
		sm := mail.SMTP{Addr: cfg.SMTPAddr, From: cfg.MailFrom, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, TLS: cfg.SMTPTLS}
		if err := sm.Validate(); err != nil {
			return nil, err
		}
		s.Mailer = sm
	} else {
		s.Outbox = &mail.Memory{}
		s.Mailer = s.Outbox
	}

	jobs := queue.NewClient(rdb)
	s.Jobs = jobs
	s.close = append(s.close, func() { jobs.Close() })

	users := postgres.NewUserRepo(db)
	md := markdown.New()
	blog := application.NewBlog(application.Repos{
		Posts:        postgres.NewPostRepo(db),
		Comments:     postgres.NewCommentRepo(db),
		Categories:   postgres.NewCategoryRepo(db),
		Users:        users,
		Social:       postgres.NewSocialRepo(db),
		Reports:      postgres.NewReportRepo(db),
		Feedback:     postgres.NewFeedbackRepo(db),
		Glossary:     postgres.NewGlossaryRepo(db),
		ClientErrors: postgres.NewClientErrorRepo(db),
		Publications: postgres.NewPublicationRepo(db),
		Stats:        postgres.NewStatsRepo(db),
		Relations:    postgres.NewRelationRepo(db),
		Revisions:    postgres.NewRevisionRepo(db),
		Series:       postgres.NewSeriesRepo(db),
		Discovery:    postgres.NewDiscoveryRepo(db),
		Lists:        postgres.NewListRepo(db),
	}, application.Ports{
		Authz:    guardauth.Authorizer{G: g},
		Audit:    guardauth.Auditor{G: g},
		Jobs:     jobs,
		Events:   redis.Events{RDB: rdb},
		Views:    redis.Views{RDB: rdb},
		Markdown: md,
		Importer: importer.New(cfg.ImportAllowPrivate),
	})
	blog.RequireVerifiedEmail = cfg.RequireVerifiedEmail
	blog.SiteURL = cfg.SiteURL
	blog.APIURL = cfg.PublicURL
	if cfg.MFAKey == "" {
		return nil, fmt.Errorf("config: MFA_KEY is required (set APP_ENV=dev for development keys)")
	}
	unsubSecret := cfg.UnsubscribeKey
	if unsubSecret == "" {
		// Legacy derivation: links emailed before UNSUBSCRIBE_KEY existed
		// were signed with a key derived from MFA_KEY.
		zap.L().Warn("UNSUBSCRIBE_KEY not set: unsubscribe links are signed with a key derived from MFA_KEY")
		unsubSecret = cfg.MFAKey
	}
	unsubKey := sha256.Sum256([]byte("unsubscribe:" + unsubSecret))
	blog.UnsubscribeKey = unsubKey[:]
	s.Blog = blog

	accounts := application.NewAccounts(users, guardauth.Identity{G: g},
		redis.ResetTokens{RDB: rdb}, redis.ResetTokens{RDB: rdb, Prefix: "verify"}, blog, cfg.SiteURL)
	if cfg.Dev() {
		zap.L().Warn("APP_ENV=dev: development secrets allowed; never run this in production")
	}
	box, err := secretbox.New(cfg.MFAKey)
	if err != nil {
		return nil, err
	}
	accounts.MFA = postgres.NewMFARepo(db)
	accounts.Challenges = redis.MFAChallenges{RDB: rdb}
	accounts.Secrets = box
	accounts.Sanctions = postgres.NewSanctionRepo(db)
	accounts.Social = postgres.NewIdentityRepo(db)
	s.Accounts = accounts

	if cfg.AdminUsername != "" && cfg.AdminEmail != "" && cfg.AdminPassword != "" {
		if err := accounts.EnsureAdmin(ctx, cfg.AdminUsername, cfg.AdminEmail, cfg.AdminPassword); err != nil {
			return nil, fmt.Errorf("seed admin: %w", err)
		}
	}

	diagramRepo := postgres.NewDiagramRepo(db)
	s.Diagrams = application.NewDiagrams(diagramRepo, diagramRepo)
	s.Diagrams.KeepLast, s.Diagrams.KeepDays = cfg.DiagramVersionsKeepLast, cfg.DiagramVersionsKeepDays
	s.Diagrams.MaxDataBytes = max(cfg.DiagramMaxProjectMB, 1) << 20
	s.Diagrams.MaxSDDBytes, s.Diagrams.MaxSDDUnzipBytes = int64(max(cfg.DiagramMaxSDDMB, 1))<<20, int64(max(cfg.DiagramMaxSDDUnzipMB, 1))<<20
	s.Diagrams.SanitizeSVG = func(b []byte) ([]byte, bool, error) {
		if !svgsafe.Looks(b) {
			return nil, false, nil
		}
		clean, err := svgsafe.Sanitize(b)
		return clean, true, err
	}
	s.Diagrams.MaxAssetBytes = max(cfg.DiagramMaxAssetMB, 1) << 20
	s.Diagrams.Users = users
	s.Diagrams.Blobs = st
	s.Diagrams.Templates = postgres.NewDiagramTemplateRepo(db)
	sender := webhooks.New(cfg.WebhookAllowPrivate, nil)
	s.Diagrams.Hooks = application.NewWebhookDispatcher(diagramRepo, jobs, sender, sender.CheckURL)
	s.Diagrams.Hooks.OnRejected = webhooks.CountBlocked
	s.Diagrams.Authz = guardauth.Authorizer{G: g}
	s.Diagrams.Audit = guardauth.Auditor{G: g}
	blog.Diagrams = s.Diagrams
	md.SetDiagrams(func(ctx context.Context, refs []string) map[string]markdown.Diagram {
		out := map[string]markdown.Diagram{}
		for ref, e := range s.Diagrams.EmbeddedDiagrams(ctx, refs) {
			d := markdown.Diagram{FileID: e.FileID, Slug: e.Slug, Title: e.Title}
			if e.ThumbnailHash != nil {
				d.ThumbnailHash = *e.ThumbnailHash
			}
			out[ref] = d
		}
		return out
	})
	s.Diagrams.Views = redis.DiagramViews{RDB: rdb}
	s.SiteStats = application.NewSiteStats(postgres.NewSiteStatsRepo(db), redis.Views{RDB: rdb}, s.Diagrams.Views, guardauth.Authorizer{G: g})
	s.Diagrams.Notices = blog // comment notifications ride the blog's notification system and its live stream

	if cfg.Analytics {
		s.Analytics = application.NewAnalytics(postgres.NewAnalyticsRepo(db), guardauth.Authorizer{G: g})
	}
	return s, nil
}
