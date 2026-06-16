package store

// ParseSQLiteTimestamp exposes the unexported parseSQLiteTimestamp helper
// so the external store_test package can pin its two accepted formats and
// the nil-on-unparseable fall-through without exporting it from the package.
var ParseSQLiteTimestamp = parseSQLiteTimestamp

// SetSessionIDForTest forces the store to mint the given session primary key so
// a test can trigger the id-PK-collision branch of CreateSession.
func (s *LiveSessionStore) SetSessionIDForTest(id string) {
	s.newID = func() string { return id }
}
