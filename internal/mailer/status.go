package mailer

// StatusView is the diagnostics-template-safe subset of SMTP config.
// Username and password are deliberately absent so credentials cannot
// reach a template.
type StatusView struct {
	Configured bool
	Host       string
	Port       int
	From       string
	TLS        bool
}

// NewStatusView returns the diagnostics view. When configured is false
// the connection fields are blanked so the template does not render
// "STARTTLS required" next to an empty host.
//
//nolint:revive // configured is a flag-shaped param matching cfg.SMTPConfigured().
func NewStatusView(cfg SMTPConfig, configured bool) StatusView {
	if !configured {
		return StatusView{Configured: false}
	}

	return StatusView{
		Configured: true,
		Host:       cfg.Host,
		Port:       cfg.Port,
		From:       cfg.From,
		TLS:        cfg.TLS,
	}
}
