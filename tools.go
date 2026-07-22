//go:build tools

// Package main pins tool dependencies so `go install` uses the versions from go.mod.
// Run `make tools` to install them.
package main

import (
	_ "google.golang.org/grpc/cmd/protoc-gen-go-grpc"
	_ "google.golang.org/protobuf/cmd/protoc-gen-go"
)
