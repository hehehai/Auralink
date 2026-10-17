package actnow

import (
	"testing"

	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/actnowkit/credentials"
)

// TestVendors: each of Auralink's services and its family vendor (spec
// §8.1, the checklist).
func TestVendors(t *testing.T) {
	for _, c := range []struct{ account, endpoint, want string }{
		{"asr.aliyun", "", "dashscope"},
		{"llm.dashscope", "", "dashscope"},
		{"llm.dashscope-intl", "", "dashscope-intl"},
		{"asr.stepfun", "", "stepfun"},
		{"llm.stepfun", "", "stepfun"},
		{"asr.doubao", "", "volcengine-speech"},
		{"llm.volcengine", "", "volcengine-ark"},
		{"asr.openai", "", "openai"},
		{"asr.openai", "https://api.openai.com/v1/audio/transcriptions", "openai"},
		{"asr.openai", "https://api.groq.com/openai/v1", ""},
		{"llm.openai", "https://example.com/v1", ""},
		{"llm.deepseek", "", "deepseek"},
		{"llm.moonshot", "", "moonshot"},
		{"llm.moonshot-intl", "", "moonshot-intl"},
		{"llm.siliconflow-intl", "", "siliconflow-intl"},
		{"llm.anthropic", "", "anthropic"},
		{"llm.openrouter", "", "openrouter"},
		{"llm.custom", "http://127.0.0.1:8080/v1", ""},
		{"llm.custom-2", "", ""},
	} {
		v, ok := Vendor(c.account, c.endpoint)
		if v.ID != c.want || ok != (c.want != "") {
			t.Errorf("%s %s: %q %v, want %q", c.account, c.endpoint, v.ID, ok, c.want)
		}
	}
}

func TestSaveOptions(t *testing.T) {
	o := SaveOptions("volcengine-speech", settings.Provider{AppID: "123456"})
	if o.AppID != "123456" || o.Auth != credentials.AuthAppIDAccessToken {
		t.Errorf("%+v", o)
	}
	if o := SaveOptions("volcengine-speech", settings.Provider{}); o != (credentials.SaveOptions{}) {
		t.Errorf("an API key: %+v", o)
	}
	if o := SaveOptions("deepseek", settings.Provider{AppID: "x"}); o != (credentials.SaveOptions{}) {
		t.Errorf("deepseek: %+v", o)
	}
	p := settings.Provider{CredentialRef: Ref("deepseek")}
	if p.CredentialRef != "actnow:vendor.deepseek" || RefVendor(p) != "deepseek" || RefVendor(settings.Provider{}) != "" {
		t.Errorf("ref %q", p.CredentialRef)
	}
}
