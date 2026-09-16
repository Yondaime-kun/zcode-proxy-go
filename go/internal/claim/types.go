package claim

type PlanEntitlement struct {
	EntitlementID string   `json:"entitlement_id"`
	ShowName      string   `json:"show_name"`
	Meter         string   `json:"meter"`
	UnitType      string   `json:"unit_type"`
	Capabilities  []string `json:"capabilities"`
	GrantUnits    int64    `json:"grant_units"`
	Period        string   `json:"period"`
	Priority      int      `json:"priority"`
	EffectiveAt   *int64   `json:"effective_at,omitempty"`
}

type ClaimablePlan struct {
	PlanID       string            `json:"plan_id"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Priority     int               `json:"priority"`
	StartsAt     *int64            `json:"starts_at,omitempty"`
	EndsAt       *int64            `json:"ends_at,omitempty"`
	Entitlements []PlanEntitlement `json:"entitlements"`
}

type ClaimOutcome struct {
	OK            bool   `json:"ok"`
	PlanID        string `json:"plan_id"`
	StartsAt      *int64 `json:"starts_at,omitempty"`
	EndsAt        *int64 `json:"ends_at,omitempty"`
	FailureKind   string `json:"failure_kind,omitempty"`
	Code          int    `json:"code,omitempty"`
	Message       string `json:"message,omitempty"`
	FailureEndsAt *int64 `json:"failure_ends_at,omitempty"`
}

func ClassifyClaimCode(code int) string {
	switch code {
	case 1001:
		return "not_found"
	case 1002:
		return "unavailable"
	case 1003:
		return "already_claimed"
	case 1004:
		return "ineligible"
	case 1005:
		return "quota_exhausted"
	case 3001:
		return "invalid_request"
	case 3007:
		return "captcha"
	case 401:
		return "login_required"
	default:
		return "unknown"
	}
}
