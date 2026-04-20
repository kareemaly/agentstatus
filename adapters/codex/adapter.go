package codex

import agentstatus "github.com/kareemaly/agentstatus"

// Adapter is the registered Codex adapter. Import for side effects:
//
//	import _ "github.com/kareemaly/agentstatus/adapters/codex"
var Adapter = agentstatus.Adapter{
	Name:           agentstatus.Codex,
	MapHookEvent:   MapHookEvent,
	InstallHooks:   installHooks,
	UninstallHooks: uninstallHooks,
}

func init() {
	if err := agentstatus.RegisterAdapter(Adapter); err != nil {
		panic(err)
	}
}
