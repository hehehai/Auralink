package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hehehai/Auralink/internal/compose"
	"github.com/hehehai/Auralink/internal/deliver"
	"github.com/hehehai/Auralink/internal/llm"
	"github.com/hehehai/Auralink/internal/store"
)

// draftEvery is how often a streaming answer is published at most.
const draftEvery = 40 * time.Millisecond

// answerSchema is Rewrite's JSON answer.
var answerSchema = func() *llm.Schema {
	s := llm.Object(map[string]*llm.Schema{"title": llm.String(), "content": llm.String()}, "title", "content")
	s.Name = "answer"
	return s
}()

// write is Translate's and Rewrite's language-model step: raw is what
// was said (Translate's speech, Rewrite's instruction; "" when Translate
// translates a selection), selection the selected text. The answer
// streams into the snapshot's Draft (and into the answer card when it
// will end there); a failure ends the session failed, in the card, to
// try again.
func (r *run) write(raw, selection string) {
	cfg := r.m.cfg
	kind := r.req.Kind
	if r.svc.LLM == nil {
		r.endWriteFailed(&Problem{Code: ProblemNoLLM, Err: errors.New("no language model is ready")}, false)
		return
	}
	snap := r.m.Current()
	in := compose.Input{
		MainLanguage: mainLanguage(r.settings, cfg.Locale()), OtherLanguages: r.settings.General.OtherLanguages,
		Glossary: r.glossary(),
	}
	var o finishOpts
	answer := false // a JSON answer {title, content}
	switch kind {
	case store.KindTranslate:
		f := r.settings.Features.Translate
		in.Task, in.TaskPrompt, in.TargetLanguage = compose.Translate, f.Prompt, snap.TargetLanguage
		if selection != "" {
			in.Text, in.Selected = selection, true
			o.needEditable = true
		} else {
			in.Text, in.Cleanup = raw, f.Cleanup
		}
		if f.ShowCard {
			o.card = deliver.ReasonCard
		}
	case store.KindRewrite:
		f := r.settings.Features.Rewrite
		in.Task, in.TaskPrompt, in.Cleanup, in.Instruction, in.Text = compose.Rewrite, f.Prompt, f.Cleanup, raw, selection
		in.Previous = r.req.Previous
		if selection != "" {
			o.needEditable = true
		} else {
			answer = true
		}
		if len(in.Previous) > 0 {
			o.card = deliver.ReasonCard // a follow-up stays in the card
		}
	}
	in.Answer = answer
	r.m.update(r, func(s *Snapshot) { s.State, s.Selection, s.Card = Composing, selection, o.card != "" && !r.req.Try })

	// Will it insert? If not, the answer streams into the card at once.
	if o.card == "" && !r.req.Try {
		ctx, cancel := context.WithTimeout(r.ctx, cfg.DeliverTime)
		res := deliver.Check(ctx, cfg.Text, deliver.Request{Text: "-", Target: r.req.Target, Self: cfg.Self, NeedEditable: o.needEditable})
		cancel()
		if res.Reason != deliver.ReasonNone && r.ctx.Err() == nil {
			r.logf("info", "the answer goes to the card: %s", res.Reason)
			o.card = res.Reason
			r.m.update(r, func(s *Snapshot) { s.Card, s.Delivery = true, res.Reason })
		}
	}

	limit := 0 // Rewrite writing from scratch: longer answers go to the card
	if kind == store.KindRewrite && selection == "" && o.card == "" {
		limit = r.settings.Features.Rewrite.CardChars
	}
	source := in.Text
	if kind == store.KindRewrite {
		source = selection
	}
	llmStart := r.now()
	res, profile, err := r.ask(in, limit)
	if err == nil && kind == store.KindTranslate && compose.Untranslated(source, res.Content, in.TargetLanguage) && r.ctx.Err() == nil {
		r.logf("warning", "the translation looks untranslated: asking again, strictly")
		r.m.update(r, func(s *Snapshot) { s.Retried, s.Draft = true, "" })
		in.Strict = true
		if again, p, err2 := r.ask(in, 0); err2 == nil {
			res, profile = again, p
		} else if r.ctx.Err() == nil {
			r.logf("warning", "the strict retry failed, keeping the first answer: %v", err2)
		}
	}
	llmTime := r.now().Sub(llmStart)
	if r.cancelled.Load() {
		r.endCancelled(nil)
		return
	}
	r.m.update(r, func(s *Snapshot) { s.Profile, s.Timings.LLM = profile, llmTime })
	if err != nil {
		r.logf("warning", "language model failed after %v: %v", llmTime.Round(time.Millisecond), err)
		r.endWriteFailed(&Problem{Code: ProblemLLM, Err: err}, true)
		return
	}
	r.logf("info", "wrote %s in %v", r.logText(res.Content), llmTime.Round(time.Millisecond))
	if res.Content == "" {
		if kind == store.KindTranslate && selection == "" {
			r.finish(Done, &Problem{Code: ProblemNoSpeech}, "", finishOpts{})
			return
		}
		r.endWriteFailed(&Problem{Code: ProblemEmpty, Err: errors.New("the language model answered nothing")}, true)
		return
	}
	r.m.update(r, func(s *Snapshot) { s.Title, s.Draft = res.Title, res.Content })
	if o.card == "" && limit > 0 && utf8.RuneCountInString(res.Content) > limit {
		o.card = deliver.ReasonLong
	}
	r.finish(Done, nil, res.Content, o)
}

// ask sends one request and streams its answer into the snapshot; limit
// (> 0) moves a longer answer into the card while it streams. It returns
// the answer (Content only, but for a JSON answer) and the composition's
// profile.
func (r *run) ask(in compose.Input, limit int) (compose.Answer, string, error) {
	cfg := r.m.cfg
	out, err := compose.Build(in)
	if err != nil {
		return compose.Answer{}, "", err
	}
	msgs := make([]llm.Message, 0, len(out.History)+2)
	for _, m := range out.Messages() {
		msgs = append(msgs, llm.Message{Role: llm.Role(m.Role), Content: m.Content})
	}
	skill := llm.SkillTranslateText
	if in.Task == compose.Rewrite {
		skill = llm.SkillRewrite
	}
	var buf strings.Builder
	var last time.Time
	carded := false
	publish := func(force bool) {
		now := time.Now()
		if !force && now.Sub(last) < draftEvery {
			return
		}
		last = now
		var a compose.Answer
		if in.Answer {
			a = compose.PartialAnswer(buf.String())
		} else {
			a.Content = strings.TrimSpace(buf.String())
		}
		toCard := limit > 0 && !carded && utf8.RuneCountInString(a.Content) > limit && !r.req.Try
		if toCard {
			carded = true
		}
		r.m.update(r, func(s *Snapshot) {
			s.Draft, s.Title = a.Content, a.Title
			if toCard {
				s.Card = true
			}
		})
	}
	req := llm.Request{
		Messages: msgs, Skill: skill, Timeout: cfg.WriteTimeout,
		OnDelta: func(d string) {
			buf.WriteString(d)
			publish(false)
		},
	}
	if in.Answer {
		req.Format, req.Schema = llm.FormatJSON, answerSchema
	}
	ctx, cancel := context.WithTimeout(r.ctx, cfg.WriteTimeout)
	defer cancel()
	resp, err := r.svc.LLM.Chat(ctx, req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded && r.ctx.Err() == nil {
			err = fmt.Errorf("no answer within %v: %w", cfg.WriteTimeout, err)
		}
		return compose.Answer{}, out.Profile(), err
	}
	if buf.Len() > 0 {
		publish(true)
	}
	if in.Answer {
		if a, ok := compose.ParseAnswer(resp.Text); ok {
			return a, out.Profile(), nil
		}
		r.logf("warning", "the answer is not the JSON asked for: taking it as text")
		return compose.Answer{Content: compose.Clean(strings.TrimSpace(strings.Trim(strings.TrimSpace(resp.Text), "`")), in.Instruction)}, out.Profile(), nil
	}
	input := in.Text
	return compose.Answer{Content: compose.Clean(resp.Text, input)}, out.Profile(), nil
}

// endWriteFailed ends a Translate or Rewrite session failed. With card,
// the answer card shows the error and offers to try again (not for a
// try); the entry is kept whatever the recording's length: it holds the
// text.
func (r *run) endWriteFailed(p *Problem, card bool) {
	r.cancel()
	r.logf("warning", "failed: %s: %v", p.Code, p.Err)
	total := r.now().Sub(r.m.Current().StartedAt)
	r.m.end(r, Failed, func(s *Snapshot) {
		s.Problem, s.Timings.Total = p, total
		s.Card = card && !r.req.Try
		s.Draft = ""
	}, func(s Snapshot) { r.keep(s, store.StatusFailed) })
}
