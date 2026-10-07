package harness

// SandboxEnv is what a Harness MAY implement to say how a launcher points the agent it
// starts at a hermetic sandbox: the harness's own spelling of the variables that relocate
// its configuration directory, its plugin cache and its temporary directory. It returns
// KEY=VALUE entries; a harness that keeps none of those in the environment does not
// implement it, and the launcher sets nothing.
type SandboxEnv interface {
	SandboxEnv(configDir, pluginCacheDir, tmpDir string) []string
}
