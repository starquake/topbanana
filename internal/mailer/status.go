package mailer

// StatusView is the diagnostics-template-safe subset of SMTP config.
// Username and password are deliberately absent so credentials cannot
// reach a template.
//
// BaseURL rides on this struct because email-link rendering can be
// broken even on an otherwise healthy SMTP deploy: the dispatchers
// silently no-op when BASE_URL is empty, so the operator needs the
// link prefix surfaced next to the SMTP wiring on the same page.
type StatusView struct {
	Configured bool
	Host       string
	Port       int
	From       string
	TLS        bool
	BaseURL    string
}

// NewStatusView returns the diagnostics view. When configured is false
// the connection fields are blanked so the template does not render
// "STARTTLS required" next to an empty host. BaseURL is populated
// regardless of configured because email links can be broken even
// when SMTP itself is healthy.
//
//nolint:revive // configured is a flag-shaped param matching cfg.SMTPConfigured().
func NewStatusView(cfg SMTPConfig, configured bool, baseURL string) StatusView {
	if !configured {
		return StatusView{Configured: false, BaseURL: baseURL}
	}

	return StatusView{
		Configured: true,
		Host:       cfg.Host,
		Port:       cfg.Port,
		From:       cfg.From,
		TLS:        cfg.TLS,
		BaseURL:    baseURL,
	}
}
