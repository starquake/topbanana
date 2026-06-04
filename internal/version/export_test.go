package version

// CommitFromSettings exposes the unexported VCS-settings parser so its
// revision / dirty / no-revision branches can be unit-tested with
// synthetic settings, independent of how the test binary was built.
var CommitFromSettings = commitFromSettings
