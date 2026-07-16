// Command clambhook-license is a small, pure-Go helper that exposes the shared
// license domain (internal/licensebridge) to the GNU/Linux GTK client and the
// terminal UI. It reads a single JSON request object from stdin and writes a
// single JSON response object to stdout, so secrets such as the license key are
// never passed on argv (which is world-readable via /proc). Every evaluation,
// date-math, and store.swiphtgroup.com HTTP call runs in Go, matching the
// gomobile bridge used by Android and the shared implementation used by macOS.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/JohnThre/clambhook/internal/licensebridge"
)

var version = "dev"

type request struct {
	Command                 string `json:"command"`
	Snapshot                string `json:"snapshot"`
	BaseURL                 string `json:"baseURL"`
	LicenseKey              string `json:"licenseKey"`
	Email                   string `json:"email"`
	Action                  string `json:"action"`
	InstallID               string `json:"installID"`
	DeviceID                string `json:"deviceID"`
	DeviceRegistration      string `json:"deviceRegistration"`
	NowUnixMillis           int64  `json:"nowUnixMillis"`
	UpdatePublishedAtMillis int64  `json:"updatePublishedAtMillis"`
	PublishedAtMillis       int64  `json:"publishedAtMillis"`
}

type response struct {
	OK      bool   `json:"ok"`
	Result  string `json:"result,omitempty"`
	Allowed bool   `json:"allowed,omitempty"`
	Error   string `json:"error,omitempty"`
}

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("clambhook-license %s\n", version)
		return
	}

	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 8<<20))
	if err != nil {
		writeResponse(response{OK: false, Error: fmt.Sprintf("read stdin: %v", err)})
		return
	}

	var req request
	if err := json.Unmarshal(raw, &req); err != nil {
		writeResponse(response{OK: false, Error: fmt.Sprintf("decode request: %v", err)})
		return
	}

	writeResponse(dispatch(req))
}

func dispatch(req request) response {
	switch req.Command {
	case "install-id":
		return response{OK: true, Result: licensebridge.NewLicenseInstallID()}
	case "portal-url":
		return response{OK: true, Result: licensebridge.LicensePortalURL()}
	case "validation-base-url":
		return response{OK: true, Result: licensebridge.LicenseValidationBaseURL()}
	case "commercial-terms":
		return stringResult(licensebridge.LicenseCommercialTermsJSON())
	case "ensure-trial":
		return stringResult(licensebridge.EnsureLicenseTrialJSON(req.Snapshot, req.NowUnixMillis))
	case "evaluate":
		return stringResult(licensebridge.EvaluateLicenseJSON(req.Snapshot, req.NowUnixMillis))
	case "status":
		return stringResult(licensebridge.LicenseStatusJSON(req.Snapshot, req.UpdatePublishedAtMillis, req.NowUnixMillis))
	case "update-allowed":
		allowed, err := licensebridge.LicenseUpdateAllowed(req.Snapshot, req.PublishedAtMillis, req.NowUnixMillis)
		if err != nil {
			return response{OK: false, Error: err.Error()}
		}
		return response{OK: true, Allowed: allowed}
	case "activate":
		return stringResult(licensebridge.ActivateLicenseJSON(req.BaseURL, req.LicenseKey, req.Email, req.DeviceRegistration, req.NowUnixMillis))
	case "device-action":
		return stringResult(licensebridge.LicenseDeviceActionJSON(req.BaseURL, req.Action, req.LicenseKey, req.InstallID, req.DeviceID, req.DeviceRegistration, req.NowUnixMillis))
	case "mark-verification-failure":
		return stringResult(licensebridge.MarkLicenseVerificationFailureJSON(req.Snapshot, req.NowUnixMillis))
	default:
		return response{OK: false, Error: fmt.Sprintf("unknown command %q", req.Command)}
	}
}

func stringResult(result string, err error) response {
	if err != nil {
		return response{OK: false, Error: err.Error()}
	}
	return response{OK: true, Result: result}
}

func writeResponse(resp response) {
	data, err := json.Marshal(resp)
	if err != nil {
		fmt.Fprintf(os.Stdout, `{"ok":false,"error":%q}`, err.Error())
		return
	}
	os.Stdout.Write(data)
	os.Stdout.Write([]byte{'\n'})
}
