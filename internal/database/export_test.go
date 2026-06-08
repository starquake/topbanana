package database

// ExportValidateSQLitePragmas exposes the unexported validateSQLitePragmas
// helper so the external database_test package can pin the DB_URI pragma
// validation (#790) without exporting it from the package.
var ExportValidateSQLitePragmas = validateSQLitePragmas
