// Package bubblehub is the Bubble Hub service. This slice is a public health
// surface only: no auth, and no BabelDOC or Zotero product logic.
package bubblehub

import "context"

// Status is the public Bubble Hub health body.
type Status struct {
	OK      bool   `json:"ok"`
	Service string `json:"service"`
	Name    string `json:"name"`
}

func status() *Status {
	return &Status{OK: true, Service: "bubblehub", Name: "Bubble Hub"}
}

// Health reports that Bubble Hub is up.
//
//encore:api public method=GET path=/bubblehub/health
func Health(ctx context.Context) (*Status, error) {
	return status(), nil
}

// Hello is the browser smoke-test for Bubble Hub. Same body as Health.
//
//encore:api public method=GET path=/bubblehub
func Hello(ctx context.Context) (*Status, error) {
	return status(), nil
}
