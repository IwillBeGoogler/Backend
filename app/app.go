// Package app is the composition root: it wires configuration,
// infrastructure, application services and HTTP into one runnable App.
package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bakhod1r/errorx"
	"github.com/bakhod1r/guard"
	"github.com/bakhod1r/guard/httpguard"
	"github.com/bakhod1r/guard/ratelimit"
	"github.com/bakhod1r/spector"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/zap"

	"github.com/iBlog/iblog-monolith-go/app/core"
	"github.com/iBlog/iblog-monolith-go/config"
	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram/topic"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/captcha"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/emqx"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/guardauth"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/introspect"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/mail"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/mqtt"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/queue"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/redis"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/schemadump"
	socialauth "github.com/iBlog/iblog-monolith-go/internal/infrastructure/social"
	"github.com/iBlog/iblog-monolith-go/internal/interfaces/diagramhub"
	httpapi "github.com/iBlog/iblog-monolith-go/internal/interfaces/http"
	"github.com/iBlog/iblog-monolith-go/internal/interfaces/http/middleware"
)

type App struct {
	Handler http.Handler
	// Metrics serves Prometheus metrics; mount it on a private listener.
	Metrics http.Handler
	// Outbox holds sent mail when SMTP is not configured (dev, tests).
	Outbox *mail.Memory

	worker *queue.Worker
	close  []func()
}

// Start runs the background worker (when enabled).
func (a *App) Start() error {
	if a.worker == nil {
		return nil
	}
	return a.worker.Start()
}

// Close stops the worker and releases connections.
func (a *App) Close() {
	if a.worker != nil {
		a.worker.Stop()
	}
	for i := len(a.close) - 1; i >= 0; i-- {
		a.close[i]()
	}
}

func New(ctx context.Context, cfg config.Config) (_ *App, err error) {
	// a is local, not the named result: "return nil, err" must not hide it
	// from the cleanup below.
	a := &App{}
	defer func() {
		if err != nil {
			a.Close()
		}
	}()

	s, err := core.New(ctx, cfg)
	if err != nil {
		return nil, err
	}
	a.close = append(a.close, s.Close)
	a.Outbox = s.Outbox
	db, rdb, g, st, jobs := s.DB, s.RDB, s.Guard, s.Storage, s.Jobs
	if err := middleware.ResetCache(ctx, rdb); err != nil {
		return nil, err
	}

	var verifyCaptcha func(ctx context.Context, token, ip string) error
	if cfg.TurnstileSecret != "" {
		verifyCaptcha = captcha.Turnstile{Secret: cfg.TurnstileSecret, URL: cfg.TurnstileVerifyURL}.Verify
	} else if !cfg.Dev() {
		zap.L().Warn("TURNSTILE_SECRET not set: signup has no CAPTCHA, only rate limits and a honeypot")
	}

	if cfg.Worker {
		a.worker = queue.NewWorker(rdb, s.Handlers())
	}

	trusted, err := cfg.TrustedProxyPrefixes()
	if err != nil {
		return nil, err
	}
	ip := middleware.ClientIP(cfg.TrustProxy, trusted)
	loc, err := time.LoadLocation(cfg.AccessTimezone)
	if err != nil {
		return nil, fmt.Errorf("ACCESS_TIMEZONE: %w", err)
	}
	gopts := httpguard.Options{
		AuthPath:  "/api/guard/auth",
		AdminPath: "/api/guard",
		ErrorLogger: func(r *http.Request, err error) {
			zap.L().Error("guard", zap.String("path", r.URL.Path), zap.Error(err))
		},
		ClientIP:     ip,
		MaxBodyBytes: 1 << 20,
	}

	// Realtime for the diagram workspace: presence, events, audio rooms and the
	// Yjs relay, over an MQTT broker (EMQX). The API instances share the work
	// through shared subscriptions; Redis keeps the audio room slots and the
	// share tokens of guest connections.
	var hub *diagramhub.Hub
	var mqttAccess *application.MQTTAccess
	if s.Diagrams != nil && cfg.MQTTURL != "" {
		ice, err := cfg.ICE()
		if err != nil {
			return nil, err
		}
		mqttAccess = &application.MQTTAccess{D: s.Diagrams, Tickets: redis.ResetTokens{RDB: rdb, Prefix: "sse"}, Guests: redis.MQTTGuests{RDB: rdb}, Clients: redis.MQTTClients{RDB: rdb},
			Kicker:     &emqx.Client{URL: cfg.EMQXAPIURL, Key: cfg.EMQXAPIKey, Secret: cfg.EMQXAPISecret},
			ServerUser: cfg.MQTTUsername, ServerPassword: cfg.MQTTPassword, CollabEnabled: cfg.CollabEnabled}
		hub = diagramhub.New(diagramhub.Options{
			Authority: mqttAccess, Slots: redis.RTCSlots{RDB: rdb}, Y: s.Diagrams.Y,
			CollabEnabled: cfg.CollabEnabled, CompactAfter: cfg.CollabCompactAfter,
			MaxUpdateBytes: s.Diagrams.MaxDataBytes + 1<<20,
			MaxPeers:       cfg.AudioPeers(), ICEServers: ice,
			OnFileGone: mqttAccess.Forget,
		})
		mqttAccess.OnDenied = hub.Denied
		mctx, stop := context.WithCancel(context.Background())
		shared, all := diagramhub.Subscriptions()
		var subs []mqtt.Subscription
		for _, f := range shared {
			subs = append(subs, mqtt.Subscription{Filter: f, QoS: 1})
		}
		for _, f := range all {
			subs = append(subs, mqtt.Subscription{Filter: f, QoS: 1})
		}
		bus, err := mqtt.Connect(mctx, mqtt.Config{URL: cfg.MQTTURL, Username: cfg.MQTTUsername, Password: cfg.MQTTPassword}, subs, func(m application.Message) {
			if m.Topic == topic.ACLDrop {
				mqttAccess.OnACLDrop(m.Payload)
				return
			}
			hub.OnMessage(m)
		})
		if err != nil {
			stop()
			hub.Close()
			return nil, err
		}
		hub.Attach(bus)
		mqttAccess.Bus = bus
		s.Diagrams.Cutoff = mqttAccess
		s.Accounts.Realtime = mqttAccess
		go func() {
			tick := time.NewTicker(time.Minute)
			defer tick.Stop()
			for {
				select {
				case <-mctx.Done():
					return
				case <-tick.C:
					if err := s.Diagrams.CutExpiredShares(mctx); err != nil && mctx.Err() == nil {
						zap.L().Warn("mqtt: cut expired share links", zap.Error(err))
					}
				}
			}
		}()
		a.close = append(a.close, stop, func() {
			cctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = bus.Close(cctx)
		}, hub.Close) // hub.Close runs first: it flushes the Yjs writes still queued
		s.Diagrams.Events = application.EventsFanout{httpapi.NewDiagramEvents(hub), s.Diagrams.Hooks}
	} else if s.Diagrams != nil && s.Diagrams.Hooks != nil {
		zap.L().Warn("MQTT_URL not set: diagram realtime is off")
		s.Diagrams.Events = s.Diagrams.Hooks
	}

	if s.Diagrams != nil {
		s.Diagrams.SchemaDump = func(ctx context.Context, prefix string) (string, error) {
			return schemadump.Dump(ctx, db, prefix)
		}
	}
	var schema diagram.SchemaIntrospector
	if cfg.SchemaIntrospect {
		schema = introspect.New(cfg.SchemaAllowPrivate)
		if cfg.SchemaAllowPrivate {
			zap.L().Warn("SCHEMA_ALLOW_PRIVATE is on: schema introspection may connect to private and loopback addresses")
		}
	}
	if cfg.WebhookAllowPrivate {
		zap.L().Warn("WEBHOOK_ALLOW_PRIVATE is on: diagram webhooks may call private and loopback addresses")
	}
	social := map[string]httpapi.SocialProvider{}
	redirect := func(name string) string {
		return strings.TrimRight(cfg.PublicURL, "/") + "/api/auth/oauth/" + name + "/callback"
	}
	if cfg.GoogleEnabled() {
		social["google"] = socialauth.NewGoogle(socialauth.Config{ClientID: cfg.GoogleClientID, ClientSecret: cfg.GoogleClientSecret,
			RedirectURL: redirect("google"), Issuer: cfg.GoogleIssuer}, rdb)
	}
	if cfg.GitHubEnabled() {
		social["github"] = socialauth.NewGitHub(socialauth.Config{ClientID: cfg.GitHubClientID, ClientSecret: cfg.GitHubClientSecret,
			RedirectURL: redirect("github"), AuthURL: cfg.GitHubAuthURL, TokenURL: cfg.GitHubTokenURL, APIURL: cfg.GitHubAPIURL}, rdb)
	}
	api := http.NewServeMux()
	httpapi.NewHandler(httpapi.Deps{
		Blog:     s.Blog,
		Accounts: s.Accounts,
		Diagrams: s.Diagrams,
		Realtime: hub, MQTT: mqttAccess, MQTTInternalSecret: cfg.MQTTInternalSecret, MQTTPublicWSPath: cfg.MQTTPublicWSPath,
		Storage: st,
		Jobs:    jobs,
		Events:  redis.Events{RDB: rdb},
		Limit:   limiter(g, cfg.RateLimitMultiplier),
		UserID:  guardauth.UserID,
		Health: map[string]func(context.Context) error{
			"postgres": db.Ping,
			"redis":    func(ctx context.Context) error { return rdb.Ping(ctx).Err() },
			"minio":    st.Ping,
		},
		SiteURL:  cfg.SiteURL,
		ClientIP: ip,
		Tickets:  redis.ResetTokens{RDB: rdb, Prefix: "sse"},

		MCPEnabled:       cfg.MCPEnabled,
		MCPRatePerMinute: cfg.MCPRatePerMinute,
		Schema:           schema,

		Analytics:      s.Analytics,
		Captcha:        verifyCaptcha,
		CaptchaSiteKey: cfg.TurnstileSiteKey,
		SiteName:       cfg.SiteName,

		Social: social,
	}).Routes(api)
	// Guard's own API: sessions, API keys, roles, permissions, ABAC policies,
	// user status (ban), audit log.
	httpguard.Mount(api, g, gopts)

	var h http.Handler = withoutGuardLogin(middleware.SpanRoute(api))
	h = middleware.Cache(rdb, h)
	h = guardauth.WithPrincipal(h)
	h = guardauth.AccessGate(g, ip, loc)(h)
	h = httpguard.Authenticate(g, gopts)(h)
	h = httpapi.TokenAuth(s.Diagrams, guardauth.UserActive(g))(h) // personal access tokens on /api/diagrams/v1 and MCP
	h = middleware.GuardFormat(h)                                 // Guard's responses in the API's format
	h = middleware.CORS(cfg.AllowedOrigins())(h)

	root := http.NewServeMux()
	root.Handle("/", h)
	mountDocs(root, cfg)
	a.Metrics = promhttp.Handler()
	if cfg.MetricsToken != "" {
		root.Handle("GET /metrics", requireBearer(cfg.MetricsToken, a.Metrics))
	}

	a.Handler = otelhttp.NewHandler(middleware.RequestID(middleware.Observe(root)), "http",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return r.Method // renamed to the matched route by middleware.SpanRoute
		}),
	)
	return a, nil
}

// requireBearer serves next only to "Authorization: Bearer <token>"
// (constant-time compare); anything else gets 404 so the endpoint is not
// advertised.
func requireBearer(token string, next http.Handler) http.Handler {
	want := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		sum := sha256.Sum256([]byte(got))
		if !ok || subtle.ConstantTimeCompare(sum[:], want[:]) != 1 {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withoutGuardLogin hides Guard's own password login: it would skip two-factor
// authentication and account restore. Clients use POST /api/auth/login.
func withoutGuardLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// PUT /password would change the password without revoking API keys
		// or recording the blog's audit event; clients use PUT /api/me/password.
		if r.URL.Path == "/api/guard/auth/login" || r.URL.Path == "/api/guard/auth/register" ||
			r.URL.Path == "/api/guard/auth/password" {
			errorx.WriteProblemRequest(w, r, errorx.New(errorx.ErrNotFound, "hidden").WithDetails("not found"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// limiter adapts Guard's Redis sliding-window limiter.
func limiter(g *guard.Guard, multiplier int) httpapi.Limiter {
	return limitWith(multiplier, func(ctx context.Context, key string, rule ratelimit.Rule) (bool, time.Duration, error) {
		res, err := g.Limiter.Allow(ctx, key, rule)
		return res.Allowed, res.RetryAfter, err
	})
}

// sensitiveLimits fail closed: without Redis, password guessing must not
// become unlimited. Everything else (likes, views) fails open.
var sensitiveLimits = []string{"login", "sso", "register", "forgot", "reset", "mfa", "verify"}

func limitWith(multiplier int, allow func(ctx context.Context, key string, rule ratelimit.Rule) (bool, time.Duration, error)) httpapi.Limiter {
	multiplier = max(multiplier, 1)
	return func(ctx context.Context, key string, n int, window time.Duration) (bool, time.Duration) {
		ok, retry, err := allow(ctx, "blog:"+key, ratelimit.Rule{Limit: n * multiplier, Window: window})
		if err == nil {
			return ok, retry
		}
		zap.L().Warn("rate limit", zap.String("key", key), zap.Error(err))
		for _, p := range sensitiveLimits {
			if strings.HasPrefix(key, p) {
				return false, 30 * time.Second
			}
		}
		return true, 0
	}
}

// mountDocs serves the Spector API console at /api/docs/. Spector builds the
// OpenAPI document from the Go source in cfg.DocsDir when the app starts, so
// the docs always match the code.
func mountDocs(mux *http.ServeMux, cfg config.Config) {
	const base = "/api/docs"
	if !cfg.Dev() && cfg.DocsKey == "" {
		zap.L().Info("API console disabled: set DOCS_KEY to enable it outside APP_ENV=dev")
		return
	}
	docs := spector.Handler(spector.Config{
		Dir:        cfg.DocsDir,
		Title:      "iBlog Blog API",
		Version:    "2.0.0",
		Adapter:    "stdlib",
		BasePath:   base,
		AccessKey:  cfg.DocsKey,
		Production: cfg.DocsProduction || !cfg.Dev(),
		Servers:    []spector.Server{{URL: cfg.PublicURL}},
		Security: map[string]spector.SecurityScheme{
			"bearerAuth": {Type: "http", Scheme: "bearer", BearerFormat: "opaque session token"},
		},
	})
	mux.HandleFunc(base, func(w http.ResponseWriter, r *http.Request) {
		target := base + "/"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery // keep ?key= on the first visit
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
	mux.Handle(base+"/", http.StripPrefix(base, docs))
}
