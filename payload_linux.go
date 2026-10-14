//go:build linux && amd64 && !lite

package main

import _ "embed"

//go:embed payload/amb-payload-linux-amd64.zip
var embeddedPayload []byte

func isWindows() bool { return false }
