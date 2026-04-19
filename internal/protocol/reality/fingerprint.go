package reality

import (
	"fmt"

	utls "github.com/refraction-networking/utls"
)

// resolveFingerprint maps a config string to a uTLS ClientHelloID. The set
// mirrors xray-core's PresetFingerprints — matching xray's map is
// intentional, since a Reality server tuned against xray expects clients
// to emit one of those browser shapes. Unknown strings fail at config
// parse time rather than connect time.
//
// Empty string defaults to Chrome, which is what xray does and what most
// Reality servers are tuned for.
func resolveFingerprint(name string) (utls.ClientHelloID, error) {
	switch name {
	case "", "chrome":
		return utls.HelloChrome_Auto, nil
	case "firefox":
		return utls.HelloFirefox_Auto, nil
	case "safari":
		return utls.HelloSafari_Auto, nil
	case "ios":
		return utls.HelloIOS_Auto, nil
	case "android":
		return utls.HelloAndroid_11_OkHttp, nil
	case "edge":
		return utls.HelloEdge_Auto, nil
	case "360":
		return utls.Hello360_Auto, nil
	case "qq":
		return utls.HelloQQ_Auto, nil
	case "random":
		return utls.HelloRandomized, nil
	case "randomized":
		return utls.HelloRandomized, nil
	case "randomizednoalpn":
		return utls.HelloRandomizedNoALPN, nil
	case "randomizedalpn":
		return utls.HelloRandomizedALPN, nil
	}
	return utls.ClientHelloID{}, fmt.Errorf("unknown fingerprint %q", name)
}
