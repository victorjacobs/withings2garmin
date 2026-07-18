package garmin

// Garmin Connect is an unofficial API. Values below were researched 2026-07-18
// against python-garminconnect 2ae0eb5 and ha-garmin 67ba9c9.
const (
	mobileSSOUserAgent = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) " +
		"AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148"
	defaultSSOBase       = "https://sso.garmin.com"
	defaultAPIBase       = "https://connectapi.garmin.com"
	defaultDIBase        = "https://diauth.garmin.com"
	iosSSOClientID       = "GCM_IOS_DARK"
	iosServiceURL        = "https://mobile.integration.garmin.com/gcm/ios"
	portalSSOClientID    = "GarminConnect"
	portalServiceURL     = "https://connect.garmin.com/app"
	diServiceTicketGrant = "https://connectapi.garmin.com/di-oauth2-service/oauth/grant/service_ticket"
)

var diClientIDs = []string{
	"GARMIN_CONNECT_MOBILE_ANDROID_DI_2025Q2",
	"GARMIN_CONNECT_MOBILE_ANDROID_DI_2024Q4",
	"GARMIN_CONNECT_MOBILE_ANDROID_DI",
	"GARMIN_CONNECT_MOBILE_IOS_DI",
}
