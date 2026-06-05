// This file intentionally makes web/ a nested Go module with no Go code.
// The frontend's node_modules ships a stray Go file
// (node_modules/flatted/golang/pkg/flatted/flatted.go); the Go tool walks
// node_modules under the root module, which is why `go test ./...` used to
// fail and the repo was pinned to `go test .`. A nested module makes the Go
// tool skip the entire web/ subtree, so `go test ./...` works from the root.
module nodepanel/web

go 1.26
