package mailer

// StatusView is the safe subset of SMTP-config knobs the diagnostics
// template renders in the "Status" panel. Username and password are
// intentionally omitted - the operator looks at host/port/from/TLS to
// confirm the wiring, the credentials are read from config only and
// must never reach a template (#321 design decision).
//
// Configured reports whether the server booted with a real mailer or
// the no-op stub so the operator can tell at a glance which mode the
// instance is in.
type StatusView struct {
	Configured bool
	Host       string
	Port       int
	From       string
	TLS        bool
}

// NewStatusView builds the safe view from an SMTPConfig. configured is
// passed in by the caller (which knows whether the SMTP block was
// populated) rather than re-derived from the SMTPConfig because the
// SMTPConfig the no-op path constructs is zero-valued by intent and
// the StatusView still needs to surface "disabled".
//
// When configured is false the connection fields (Host, Port, From,
// TLS) are blanked in the returned view. SMTPTLS defaults to true in
// config.Parse so an unconfigured deploy would otherwise render
// "STARTTLS required" next to host="", which is misleading: there is
// no connection to require anything of. The caller's SMTPConfig is
// not mutated; the masking happens on the copy returned here.
//
// configured is a one-bit input the caller already computed via
// cfg.SMTPConfigured(); folding the boolean back into the struct
// would either rename it (still flag-shaped) or split this into two
// constructors with no callsite reduction. The current shape is the
// simplest one that keeps the no-op path coherent.
//
//nolint:revive // see paragraph above for the flag-parameter rationale.
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
