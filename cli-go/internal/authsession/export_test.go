package authsession

// export_test.go — test-only exports for the authsession package.
// Exposes the session cookie name constant so cross-package tests can
// assert that sessionauth.go and authsession agree on the name.

// CookieNameSession is the exported name of the session cookie, exposed
// for cross-package tests only (e.g. the consoleui test that asserts the
// hardcoded literal in sessionauth.go matches the authoritative constant here).
const CookieNameSession = cookieNameSession
