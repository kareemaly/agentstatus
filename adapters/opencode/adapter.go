package opencode

import "github.com/kareemaly/agentstatus"

func init() {
	if err := agentstatus.RegisterAdapter(Adapter); err != nil {
		panic(err)
	}
}

var Adapter = agentstatus.Adapter{
	Name:           agentstatus.OpenCode,
	MapHookEvent:   MapHookEvent,
	InstallHooks:   installHooks,
	UninstallHooks: uninstallHooks,
}
