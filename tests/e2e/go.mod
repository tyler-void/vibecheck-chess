// E2E scaffold module for the KISS-Chess Go backend.
//
// TODO: once src/server has a stable module path (e.g.
// `kisschess/src/server` or `github.com/<org>/kisschess/server`), add it as a
// require + replace directive here so startServer() can mount the real
// handler. For now this module has no external dependencies; tests skip.
module kisschess/tests/e2e

go 1.22
