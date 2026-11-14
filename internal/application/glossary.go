package application

import (
	"context"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/glossary"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/user"
)

// Glossary terms: reads are public, writes need glossary.write.

func (b *Blog) ListGlossary(ctx context.Context, lang string) ([]glossary.Term, error) {
	ctx, span := tracer.Start(ctx, "Blog.ListGlossary")
	defer span.End()
	return b.Glossary.List(ctx, lang)
}

func (b *Blog) CreateGlossaryTerm(ctx context.Context, actor user.User, in glossary.Input) (glossary.Term, error) {
	ctx, span := tracer.Start(ctx, "Blog.CreateGlossaryTerm")
	defer span.End()
	if err := b.Authz.Can(ctx, "glossary", "write", 0, 0); err != nil {
		return glossary.Term{}, err
	}
	in, err := in.Normalize()
	if err != nil {
		return glossary.Term{}, err
	}
	t, err := b.Glossary.Create(ctx, in)
	if err == nil {
		b.Audit.Record(ctx, actor.ID, "glossary.create", "glossary:"+itoa(t.ID), map[string]any{"term": t.Term})
	}
	return t, err
}

func (b *Blog) UpdateGlossaryTerm(ctx context.Context, actor user.User, id int, in glossary.Input) (glossary.Term, error) {
	ctx, span := tracer.Start(ctx, "Blog.UpdateGlossaryTerm")
	defer span.End()
	if err := b.Authz.Can(ctx, "glossary", "write", id, 0); err != nil {
		return glossary.Term{}, err
	}
	in, err := in.Normalize()
	if err != nil {
		return glossary.Term{}, err
	}
	t, err := b.Glossary.Update(ctx, id, in)
	if err == nil {
		b.Audit.Record(ctx, actor.ID, "glossary.update", "glossary:"+itoa(id), map[string]any{"term": t.Term})
	}
	return t, err
}

func (b *Blog) DeleteGlossaryTerm(ctx context.Context, actor user.User, id int) error {
	ctx, span := tracer.Start(ctx, "Blog.DeleteGlossaryTerm")
	defer span.End()
	if err := b.Authz.Can(ctx, "glossary", "write", id, 0); err != nil {
		return err
	}
	err := b.Glossary.Delete(ctx, id)
	if err == nil {
		b.Audit.Record(ctx, actor.ID, "glossary.delete", "glossary:"+itoa(id), nil)
	}
	return err
}
