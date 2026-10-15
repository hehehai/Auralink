// Package compose is Auralink's prompt composer (plan §4.3): it builds the
// one language-model request of a session from layers, in a fixed order,
// instead of a long prompt per feature that repeats the same rules (Voxt's
// docs/TextEnhancementPromptLayeringPlan, which Voxt itself never landed):
//
//	system = Role            what Auralink is doing
//	       + Input policy    what the input is (speech, a selection, an instruction)
//	       + [Cleanup]       basic clean-up of speech recognition
//	       + [Structure]     lists and paragraphs (Transcribe only), or Form: keep it as said
//	       + Task            the feature's rules, then the user's addition to them
//	       + [Scene]         app enhancement: the app and the group's rules (M5)
//	       + [Glossary]      the dictionary's terms
//	       + Output contract only the final text
//	user   = <input>…</input>
//
// The input text is only ever in the user message, never in the system
// prompt (no {{RAW_TRANSCRIPTION}}): the system prompt stays the same for
// the same settings, and dictated text cannot pose as rules.
//
// The rules are embedded templates in two sets, English and Simplified
// Chinese, chosen by the user's main language. Variables, in the
// templates and in the user's own prompts: {{USER_MAIN_LANGUAGE}},
// {{USER_OTHER_LANGUAGES}}, {{TARGET_LANGUAGE}}, {{APP_NAME}};
// "{{#if NAME}}…{{/if}}" keeps its text only when NAME is not empty.
//
// M3 composes Transcribe; Translate and Rewrite (M4) fit the same Input
// and return ErrTask until their layers exist.
//
// Imports: lang.
package compose

import (
	"embed"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/hehehai/Auralink/internal/lang"
)

// Task is the feature a request is for.
type Task string

const (
	Transcribe Task = "transcribe"
	Translate  Task = "translate"
	Rewrite    Task = "rewrite"
)

// ErrTask: the task has no layers yet.
var ErrTask = errors.New("compose: task not supported yet")

// Template sets.
const (
	English = "en"
	Chinese = "zh-Hans"
)

// Term is a glossary entry: a term as it must be written and what speech
// recognition tends to make of it.
type Term struct {
	Term    string
	Aliases []string
}

// Scene is app enhancement's layer: the app in front and the matched
// group's prompt (M5).
type Scene struct {
	AppName string
	Prompt  string
}

// Input is everything a request is built from.
type Input struct {
	Task Task
	// Cleanup and Structure are the capability layers the feature has on
	// (settings.Transcribe.Cleanup, Structure; Structure only for
	// Transcribe).
	Cleanup   bool
	Structure bool
	// TaskPrompt is the user's addition to the task's rules
	// (settings.Feature.Prompt); "" for none.
	TaskPrompt string
	// Scene is the matched app enhancement group; nil for none.
	Scene *Scene
	// Glossary are the dictionary's enabled terms (Build keeps the ones
	// that fit, those found in Text first).
	Glossary []Term
	// MainLanguage and OtherLanguages are lang codes ("zh", "en"):
	// settings.General's, with "" resolved by the caller. TargetLanguage
	// is a translation's (M4).
	MainLanguage   string
	OtherLanguages []string
	TargetLanguage string
	// Text is the input: the recognised speech (Transcribe), the
	// selection (Translate, Rewrite).
	Text string
	// Instruction is Rewrite's spoken instruction (M4).
	Instruction string
}

// Output is a composed request.
type Output struct {
	System string
	User   string
	// Language is the template set used (English, Chinese).
	Language string
	// Layers names the layers in the system prompt, in order; Profile
	// joins them for logs and history ("transcribe:cleanup+structure").
	Layers []string
}

// Profile is a short name of the composition.
func (o Output) Profile() string {
	if len(o.Layers) == 0 {
		return ""
	}
	task, rest := o.Layers[0], o.Layers[1:]
	var caps []string
	for _, l := range rest {
		switch l {
		case "cleanup", "structure", "custom", "scene", "glossary":
			caps = append(caps, l)
		}
	}
	if len(caps) == 0 {
		return task
	}
	return task + ":" + strings.Join(caps, "+")
}

//go:embed templates
var templates embed.FS

func template(set, name string) string {
	b, err := templates.ReadFile("templates/" + set + "/" + name + ".txt")
	if err != nil {
		panic(fmt.Sprintf("compose: no template %s/%s", set, name))
	}
	return strings.TrimRight(string(b), "\n")
}

// TemplateSet is the set of rules for a main language: Chinese for
// Chinese and Cantonese, English for every other language.
func TemplateSet(mainLanguage string) string {
	switch lang.Canonical(mainLanguage) {
	case "zh", "yue":
		return Chinese
	}
	return English
}

// Limits of the glossary.
const (
	MaxGlossaryTerms = 60
	MaxGlossaryChars = 2000
)

// Build composes the request of in.
func Build(in Input) (Output, error) {
	if in.Task != Transcribe {
		return Output{}, fmt.Errorf("%w: %s", ErrTask, in.Task)
	}
	set := TemplateSet(in.MainLanguage)
	vars := Variables(in, set)
	var parts, layers []string
	add := func(layer, text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		parts = append(parts, text)
		layers = append(layers, layer)
	}
	render := func(name string, extra map[string]string) string {
		return Render(template(set, name), vars, extra)
	}

	add(string(in.Task), render("role-transcribe", nil))
	add("input", render("input-speech", nil))
	if in.Cleanup {
		add("cleanup", render("cleanup", nil))
	}
	if in.Structure {
		add("structure", render("structure", nil))
	} else {
		add("form", render("form", nil))
	}
	add("task", render("task-transcribe", nil))
	if p := strings.TrimSpace(in.TaskPrompt); p != "" {
		add("custom", render("custom", map[string]string{"TASK_PROMPT": Render(p, vars, nil)}))
	}
	if s := in.Scene; s != nil && strings.TrimSpace(s.AppName) != "" {
		add("scene", render("scene", map[string]string{"SCENE_PROMPT": Render(strings.TrimSpace(s.Prompt), vars, nil)}))
	}
	if g := Glossary(in.Glossary, in.Text, set); g != "" {
		add("glossary", render("glossary", map[string]string{"GLOSSARY": g}))
	}
	add("output", render("output-transcribe", nil))

	return Output{System: strings.Join(parts, "\n\n"), User: UserMessage(in.Text), Language: set, Layers: layers}, nil
}

// UserMessage wraps the input for the user message.
func UserMessage(text string) string {
	text = strings.ReplaceAll(strings.TrimSpace(text), "</input>", "<\\/input>")
	return "<input>\n" + text + "\n</input>"
}

// Variables are the public variables of in, in template set set's
// language.
func Variables(in Input, set string) map[string]string {
	name := func(code string) string {
		if l, ok := lang.Lookup(code); ok {
			return l.NameIn(set)
		}
		return code
	}
	sep := ", "
	if set == Chinese {
		sep = "、"
	}
	var others []string
	for _, c := range in.OtherLanguages {
		if c = strings.TrimSpace(c); c != "" && lang.Canonical(c) != lang.Canonical(in.MainLanguage) {
			others = append(others, name(c))
		}
	}
	app := ""
	if in.Scene != nil {
		app = strings.TrimSpace(in.Scene.AppName)
	}
	main := name(in.MainLanguage)
	if strings.TrimSpace(in.MainLanguage) == "" {
		main = name("en")
	}
	return map[string]string{
		"USER_MAIN_LANGUAGE":   main,
		"USER_OTHER_LANGUAGES": strings.Join(others, sep),
		"TARGET_LANGUAGE":      name(in.TargetLanguage),
		"APP_NAME":             app,
	}
}

// VariableNames are the variables a user's prompt may use, for the
// feature pages.
var VariableNames = []string{"USER_MAIN_LANGUAGE", "USER_OTHER_LANGUAGES", "TARGET_LANGUAGE", "APP_NAME"}

var ifBlock = regexp.MustCompile(`(?s)\{\{#if ([A-Z_]+)\}\}(.*?)\{\{/if\}\}`)

// Render fills a template: "{{#if NAME}}…{{/if}}" blocks, then
// "{{NAME}}" from vars and extra, in one pass (a value is never expanded
// again). Unknown names stay as written.
func Render(tpl string, vars, extra map[string]string) string {
	get := func(k string) (string, bool) {
		if v, ok := extra[k]; ok {
			return v, true
		}
		v, ok := vars[k]
		return v, ok
	}
	tpl = ifBlock.ReplaceAllStringFunc(tpl, func(m string) string {
		sub := ifBlock.FindStringSubmatch(m)
		if v, _ := get(sub[1]); strings.TrimSpace(v) != "" {
			return sub[2]
		}
		return ""
	})
	var pairs []string
	for k, v := range vars {
		if _, over := extra[k]; !over {
			pairs = append(pairs, "{{"+k+"}}", v)
		}
	}
	for k, v := range extra {
		pairs = append(pairs, "{{"+k+"}}", v)
	}
	return strings.NewReplacer(pairs...).Replace(tpl)
}

// Glossary lists the terms that fit the limits, one per line ("- Term
// (heard as: alias, alias)"): terms found in text first (their spelling
// or an alias, ignoring case and spaces), then the others in their order.
// "" when there are none.
func Glossary(terms []Term, text, set string) string {
	norm := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) || r == '-' || r == '_' {
				return -1
			}
			return unicode.ToLower(r)
		}, s)
	}
	ntext := norm(text)
	found := func(t Term) bool {
		if ntext == "" {
			return false
		}
		for _, s := range append([]string{t.Term}, t.Aliases...) {
			if n := norm(s); n != "" && strings.Contains(ntext, n) {
				return true
			}
		}
		return false
	}
	var first, rest []Term
	seen := map[string]bool{}
	for _, t := range terms {
		t.Term = strings.TrimSpace(t.Term)
		if t.Term == "" || seen[strings.ToLower(t.Term)] {
			continue
		}
		seen[strings.ToLower(t.Term)] = true
		if found(t) {
			first = append(first, t)
		} else {
			rest = append(rest, t)
		}
	}
	heard := "heard as"
	sep := ", "
	if set == Chinese {
		heard, sep = "可能被听成", "、"
	}
	var lines []string
	size := 0
	for _, t := range append(first, rest...) {
		line := "- " + t.Term
		var aliases []string
		for _, a := range t.Aliases {
			if a = strings.TrimSpace(a); a != "" && !strings.EqualFold(a, t.Term) {
				aliases = append(aliases, a)
			}
		}
		if len(aliases) > 0 {
			if set == Chinese {
				line += "（" + heard + "：" + strings.Join(aliases, sep) + "）"
			} else {
				line += " (" + heard + ": " + strings.Join(aliases, sep) + ")"
			}
		}
		if len(lines) == MaxGlossaryTerms || size+len(line) > MaxGlossaryChars {
			break
		}
		lines = append(lines, line)
		size += len(line) + 1
	}
	return strings.Join(lines, "\n")
}

// Clean tidies a model's answer to input: without the <input> tags a
// model echoed, or the quotes it wrapped the whole answer in (unless the
// input was quoted so itself).
func Clean(out, input string) string {
	s := strings.TrimSpace(out)
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, "<input>"), "</input>"))
	in := strings.TrimSpace(input)
	for _, q := range [][2]string{{`"`, `"`}, {"“", "”"}, {"「", "」"}} {
		wrapped := func(t string) bool {
			return len(t) > len(q[0])+len(q[1]) && strings.HasPrefix(t, q[0]) && strings.HasSuffix(t, q[1]) &&
				!strings.Contains(t[len(q[0]):len(t)-len(q[1])], q[0])
		}
		if wrapped(s) && !wrapped(in) {
			s = strings.TrimSpace(s[len(q[0]) : len(s)-len(q[1])])
		}
	}
	return s
}
