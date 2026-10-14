//go:build windows && amd64

package main

import _ "embed"

//go:embed payload/amb-payload-windows-amd64.zip
var embeddedPayload []byte

func isWindows() bool { return true }
