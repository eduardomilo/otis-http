package services

import (
	"fmt"

	"github.com/otis-http/otis/internal/git"
)

// GitService reports the state of the repository the open collection lives in.
// It is read-only: Otis shows what git thinks and leaves committing to git.
//
// The state also travels with the tree, because a file's status changes at the
// same moment the file does. This service is for the window asking on its own
// — after an error, or when it wants the counts without a re-walk.
//
// The collection root lives in CollectionService, so this service borrows it
// rather than keeping a second copy that could disagree.
type GitService struct {
	collections *CollectionService
}

// NewGitService constructs the service.
func NewGitService(collections *CollectionService) *GitService {
	return &GitService{collections: collections}
}

// State reads the repository around the open collection. A collection that is
// not in a repository yields State{Repository: false} and no error.
func (s *GitService) State() (git.State, error) {
	root := s.collections.Current().Path
	if root == "" {
		return git.State{}, fmt.Errorf("no collection is open")
	}
	return git.Read(root)
}
