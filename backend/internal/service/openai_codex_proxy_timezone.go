package service

import (
	"context"
	"strings"
)

// codexProxyCountryTimezones is a fallback for proxy latency cache entries
// written before the probe started persisting ip-api's timezone field. The
// cached timezone is always preferred because a country can span multiple
// timezones.
var codexProxyCountryTimezones = map[string]string{
	"AE": "Asia/Dubai",
	"AR": "America/Argentina/Buenos_Aires",
	"AT": "Europe/Vienna",
	"AU": "Australia/Sydney",
	"BD": "Asia/Dhaka",
	"BE": "Europe/Brussels",
	"BG": "Europe/Sofia",
	"BR": "America/Sao_Paulo",
	"CA": "America/Toronto",
	"CH": "Europe/Zurich",
	"CL": "America/Santiago",
	"CN": "Asia/Shanghai",
	"CO": "America/Bogota",
	"CZ": "Europe/Prague",
	"DE": "Europe/Berlin",
	"DK": "Europe/Copenhagen",
	"EG": "Africa/Cairo",
	"ES": "Europe/Madrid",
	"FI": "Europe/Helsinki",
	"FR": "Europe/Paris",
	"GB": "Europe/London",
	"GR": "Europe/Athens",
	"HK": "Asia/Hong_Kong",
	"HU": "Europe/Budapest",
	"ID": "Asia/Jakarta",
	"IE": "Europe/Dublin",
	"IL": "Asia/Jerusalem",
	"IN": "Asia/Kolkata",
	"IT": "Europe/Rome",
	"JP": "Asia/Tokyo",
	"KR": "Asia/Seoul",
	"KZ": "Asia/Almaty",
	"MA": "Africa/Casablanca",
	"MX": "America/Mexico_City",
	"MY": "Asia/Kuala_Lumpur",
	"NG": "Africa/Lagos",
	"NL": "Europe/Amsterdam",
	"NO": "Europe/Oslo",
	"NZ": "Pacific/Auckland",
	"PH": "Asia/Manila",
	"PK": "Asia/Karachi",
	"PL": "Europe/Warsaw",
	"PT": "Europe/Lisbon",
	"RO": "Europe/Bucharest",
	"RS": "Europe/Belgrade",
	"RU": "Europe/Moscow",
	"SA": "Asia/Riyadh",
	"SE": "Europe/Stockholm",
	"SG": "Asia/Singapore",
	"TH": "Asia/Bangkok",
	"TR": "Europe/Istanbul",
	"TW": "Asia/Taipei",
	"UA": "Europe/Kyiv",
	"UK": "Europe/London",
	"US": "America/Los_Angeles",
	"VN": "Asia/Ho_Chi_Minh",
	"ZA": "Africa/Johannesburg",
}

// codexEnvironmentContextTimezone resolves the timezone that should be sent in
// the Codex environment context. An account-level override wins, followed by
// the effective proxy's exit timezone. The fixed Codex/OpenAI default remains
// the final fallback for direct connections and cache misses.
func (s *OpenAIGatewayService) codexEnvironmentContextTimezone(ctx context.Context, account *Account) string {
	if account != nil {
		if timezone := normalizeCodexEnvironmentContextTimezone(account.GetExtraString(codexEnvironmentContextTimezoneExtraKey)); timezone != "" {
			return timezone
		}
	}
	if s == nil || s.proxyLatencyCache == nil || account == nil || account.ProxyID == nil || *account.ProxyID <= 0 || ctx == nil {
		return codexEnvironmentContextDefaultTimezone
	}

	latencies, err := s.proxyLatencyCache.GetProxyLatencies(ctx, []int64{*account.ProxyID})
	if err != nil {
		return codexEnvironmentContextDefaultTimezone
	}
	if timezone := codexProxyTimezoneFromLatency(latencies[*account.ProxyID]); timezone != "" {
		return timezone
	}
	return codexEnvironmentContextDefaultTimezone
}

func codexProxyTimezoneFromLatency(info *ProxyLatencyInfo) string {
	if info == nil {
		return ""
	}
	if timezone := normalizeCodexEnvironmentContextTimezone(info.Timezone); timezone != "" {
		return timezone
	}
	return codexProxyCountryTimezones[strings.ToUpper(strings.TrimSpace(info.CountryCode))]
}
