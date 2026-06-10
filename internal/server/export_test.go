package server

var (
	ExportAddRoutes         = addRoutes
	ExportLogRequests       = logRequests
	ExportRecoverPanic      = recoverPanic
	ExportRequestLogger     = requestLogger
	ExportLoggerFrom        = loggerFrom
	ExportSameOriginCheck   = sameOriginCheck
	ExportOriginFromBaseURL = originFromBaseURL
)
