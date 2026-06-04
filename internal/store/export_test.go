package store

// ParseSQLiteTimestamp exposes the unexported parseSQLiteTimestamp helper
// so the external store_test package can pin its two accepted formats and
// the nil-on-unparseable fall-through without exporting it from the package.
var ParseSQLiteTimestamp = parseSQLiteTimestamp
