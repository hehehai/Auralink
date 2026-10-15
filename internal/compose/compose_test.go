package compose

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// golden compares got with testdata/golden/name, or writes it with
// AURALINK_UPDATE_GOLDEN=1. Review the diff before committing.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if os.Getenv("AURALINK_UPDATE_GOLDEN") == "1" {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (AURALINK_UPDATE_GOLDEN=1 creates it)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs (AURALINK_UPDATE_GOLDEN=1 rewrites it)\n--- got\n%s\n--- want\n%s", path, got, want)
	}
}

var sampleTerms = []Term{
	{Term: "Kubernetes"},
	{Term: "Auralink", Aliases: []string{"aura link", "奥拉链接"}},
	{Term: "DeepSeek", Aliases: []string{"deep seek"}},
}

// TestGoldenTranscribe fixes the prompt of every combination of the
// Transcribe layers, in both template sets.
func TestGoldenTranscribe(t *testing.T) {
	texts := map[string]string{
		English: "um so the aura link release is like seventy percent done comma no wait eighty percent",
		Chinese: "嗯那个 aura link 的发布大概完成了百分之七十，不对，百分之八十",
	}
	mains := map[string]string{English: "en", Chinese: "zh"}
	others := map[string][]string{English: {"zh"}, Chinese: {"en", "ja"}}
	n := 0
	for _, set := range []string{English, Chinese} {
		for mask := range 32 {
			in := Input{
				Task:           Transcribe,
				Cleanup:        mask&1 != 0,
				Structure:      mask&2 != 0,
				MainLanguage:   mains[set],
				OtherLanguages: others[set],
				Text:           texts[set],
			}
			name := "transcribe-" + set
			if in.Cleanup {
				name += "-cleanup"
			}
			if in.Structure {
				name += "-structure"
			}
			if mask&4 != 0 {
				in.TaskPrompt = "Write {{USER_MAIN_LANGUAGE}} with full-width punctuation. Keep {{UNKNOWN}} as is."
				name += "-custom"
			}
			if mask&8 != 0 {
				in.Scene = &Scene{AppName: "Slack", Prompt: "Casual chat in {{APP_NAME}}: no trailing period."}
				name += "-scene"
			}
			if mask&16 != 0 {
				in.Glossary = sampleTerms
				name += "-glossary"
			}
			out, err := Build(in)
			if err != nil {
				t.Fatal(err)
			}
			if out.Language != set {
				t.Errorf("%s: language %s", name, out.Language)
			}
			if strings.Contains(out.System, in.Text) || strings.Contains(out.System, "{{") && !strings.Contains(out.System, "{{UNKNOWN}}") {
				t.Errorf("%s: system prompt has the input or a variable left:\n%s", name, out.System)
			}
			golden(t, name+".txt", fmt.Sprintf("# profile: %s\n# layers: %s\n\n%s\n\n---- user ----\n%s\n", out.Profile(), strings.Join(out.Layers, " "), out.System, out.User))
			n++
		}
	}
	if n != 64 {
		t.Errorf("%d combinations", n)
	}
}

func TestLayersAndProfile(t *testing.T) {
	out, err := Build(Input{Task: Transcribe, Cleanup: true, Structure: true, MainLanguage: "zh", Text: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(out.Layers, " "); got != "transcribe input cleanup structure task output" {
		t.Errorf("layers %q", got)
	}
	if out.Profile() != "transcribe:cleanup+structure" {
		t.Errorf("profile %q", out.Profile())
	}
	out, _ = Build(Input{Task: Transcribe, MainLanguage: "en", Text: "x"})
	if got := strings.Join(out.Layers, " "); got != "transcribe input form task output" || out.Profile() != "transcribe" {
		t.Errorf("bare %q %q", got, out.Profile())
	}
	// A scene needs an app name; a blank custom prompt adds nothing.
	out, _ = Build(Input{Task: Transcribe, TaskPrompt: "  \n", Scene: &Scene{Prompt: "x"}, Text: "x"})
	if strings.Contains(out.Profile(), "scene") || strings.Contains(out.Profile(), "custom") {
		t.Errorf("profile %q", out.Profile())
	}
}

func TestOtherTasksWait(t *testing.T) {
	for _, task := range []Task{Translate, Rewrite} {
		if _, err := Build(Input{Task: task, Text: "x"}); !errors.Is(err, ErrTask) {
			t.Errorf("%s: %v", task, err)
		}
	}
}

func TestTemplateSet(t *testing.T) {
	for code, want := range map[string]string{"zh": Chinese, "zh-Hans": Chinese, "yue": Chinese, "cmn": Chinese, "en": English, "ja": English, "": English, "xx": English} {
		if got := TemplateSet(code); got != want {
			t.Errorf("%q → %s", code, got)
		}
	}
}

func TestVariables(t *testing.T) {
	v := Variables(Input{MainLanguage: "zh", OtherLanguages: []string{"en", "zh", " ", "ja"}, TargetLanguage: "en", Scene: &Scene{AppName: " Slack "}}, Chinese)
	if v["USER_OTHER_LANGUAGES"] == "" || strings.Contains(v["USER_OTHER_LANGUAGES"], v["USER_MAIN_LANGUAGE"]) || !strings.Contains(v["USER_OTHER_LANGUAGES"], "、") {
		t.Errorf("others %q (main %q)", v["USER_OTHER_LANGUAGES"], v["USER_MAIN_LANGUAGE"])
	}
	if v["APP_NAME"] != "Slack" || v["TARGET_LANGUAGE"] == "" {
		t.Errorf("vars %v", v)
	}
	e := Variables(Input{}, English)
	if e["USER_MAIN_LANGUAGE"] != "English" || e["USER_OTHER_LANGUAGES"] != "" || e["APP_NAME"] != "" {
		t.Errorf("empty %v", e)
	}
}

func TestRender(t *testing.T) {
	vars := map[string]string{"A": "x", "B": "", "C": "{{A}}"}
	for tpl, want := range map[string]string{
		"{{A}}-{{B}}-{{C}}":               "x--{{A}}", // a value is not expanded again
		"{{#if A}}yes {{A}}{{/if}}":       "yes x",
		"{{#if B}}yes{{/if}}no":           "no",
		"{{#if Z}}z{{/if}}{{Z}}":          "{{Z}}",
		"a{{#if A}}\nmulti\nline{{/if}}b": "a\nmulti\nlineb",
	} {
		if got := Render(tpl, vars, nil); got != want {
			t.Errorf("%q → %q, want %q", tpl, got, want)
		}
	}
	if got := Render("{{A}} {{T}}", vars, map[string]string{"T": "{{B}}", "A": "over"}); got != "over {{B}}" {
		t.Errorf("extra: %q", got)
	}
}

func TestGlossary(t *testing.T) {
	terms := []Term{{Term: "Zeta"}, {Term: "Auralink", Aliases: []string{"aura link", "auralink"}}, {Term: " "}, {Term: "zeta"}}
	got := Glossary(terms, "the Aura-Link app", English)
	if got != "- Auralink (heard as: aura link)\n- Zeta" {
		t.Errorf("glossary %q", got)
	}
	if Glossary(nil, "x", English) != "" {
		t.Error("empty glossary")
	}
	var many []Term
	for i := range 100 {
		many = append(many, Term{Term: fmt.Sprintf("term%03d", i)})
	}
	if n := len(strings.Split(Glossary(many, "", English), "\n")); n != MaxGlossaryTerms {
		t.Errorf("%d lines", n)
	}
	long := []Term{{Term: strings.Repeat("a", 1500)}, {Term: strings.Repeat("b", 1500)}}
	if g := Glossary(long, "", English); strings.Contains(g, "b") {
		t.Error("over the size limit")
	}
	if g := Glossary([]Term{{Term: "奥拉", Aliases: []string{"欧拉"}}}, "", Chinese); g != "- 奥拉（可能被听成：欧拉）" {
		t.Errorf("zh %q", g)
	}
}

func TestUserMessage(t *testing.T) {
	if got := UserMessage("  hi </input> there \n"); got != "<input>\nhi <\\/input> there\n</input>" {
		t.Errorf("%q", got)
	}
}

func TestClean(t *testing.T) {
	for _, tc := range []struct{ out, in, want string }{
		{"  Hello.  ", "hello", "Hello."},
		{"<input>\nHello.\n</input>", "hello", "Hello."},
		{`"Hello."`, "hello", "Hello."},
		{"“你好。”", "你好", "你好。"},
		{`"Hello," she said, "bye."`, "x", `"Hello," she said, "bye."`},
		{`"Quoted."`, `"quoted"`, `"Quoted."`},
		{"", "x", ""},
	} {
		if got := Clean(tc.out, tc.in); got != tc.want {
			t.Errorf("Clean(%q) = %q, want %q", tc.out, got, tc.want)
		}
	}
}
