// Package zotero exposes the Zotero-plugin HTTP contract (no auth in this slice).
package zotero

//encore:service
type Service struct{}

func initService() (*Service, error) {
	return &Service{}, nil
}
