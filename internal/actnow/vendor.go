package actnow

import (
	"github.com/hehehai/Auralink/internal/settings"
	"github.com/hehehai/actnowkit/credentials"
)

// Vendor is the family vendor whose key a service of Auralink uses
// (spec §8.1), by its Keychain account ("asr.aliyun", "llm.deepseek") and
// its endpoint ("" for the preset's): DashScope for both Alibaba Cloud
// services, StepFun for both of StepFun's, Volcano Engine Ark for the
// language models and Doubao speech for the speech service apart, OpenAI
// only at api.openai.com. Custom services (custom-N) share none.
func Vendor(account, endpoint string) (credentials.Vendor, bool) {
	return credentials.VendorForLegacy(AppID, account, endpoint)
}

// SaveOptions are the family's fields of a service's key: Doubao speech's
// App ID, with which its key is an access token (spec §8.2).
func SaveOptions(vendor string, p settings.Provider) credentials.SaveOptions {
	if vendor == "volcengine-speech" && p.AppID != "" {
		return credentials.SaveOptions{AppID: p.AppID, Auth: credentials.AuthAppIDAccessToken}
	}
	return credentials.SaveOptions{}
}

// Ref is a service's credentialRef for the family's key of vendor, and
// RefVendor its vendor ("" when the service uses its own key).
func Ref(vendor string) string { return credentials.Ref(vendor) }

func RefVendor(p settings.Provider) string {
	v, _ := credentials.ParseRef(p.CredentialRef)
	return v
}
