// Package pluginenv names the environment unison's two modes share.
//
// It is its own package because both ends need it and neither should import the
// other: plugin mode reads these out of its environment, and the orchestrator
// documents them.
package pluginenv

// LogLevelEnvVar sets the log level for plugin mode.
//
// Plugin mode has no flags to take a level from, because sqlc chooses the
// arguments it invokes a plugin with, so an environment variable is the only
// channel left.
//
// It is worth knowing what it does and does not buy. sqlc captures a plugin's
// stderr and includes it only in the error it reports when the plugin fails, so
// on a successful run these logs are discarded and setting this achieves
// nothing. When generation fails, a higher level puts more of unison's reasoning
// into the message sqlc prints. It is a debugging aid for a failure, not a
// progress display.
const LogLevelEnvVar = "UNISON_LOG_LEVEL"
