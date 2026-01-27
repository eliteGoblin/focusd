package policy

import (
	"fmt"

	"github.com/eliteGoblin/focusd/app_mon/internal/domain"
)

// Registry holds all app blocking policies.
// This is the in-memory policy store for MVP.
// Future: Can be replaced with cloud-backed store.
type Registry struct {
	policies map[string]AppPolicy
}

// NewRegistry creates a registry with all default policies.
func NewRegistry() *Registry {
	r := &Registry{
		policies: make(map[string]AppPolicy),
	}

	// Register default policies
	r.Register(NewSteamPolicy())
	r.Register(NewDota2Policy())

	return r
}

// NewRegistryWithPolicies creates a registry with custom policies (for testing).
func NewRegistryWithPolicies(policies ...AppPolicy) *Registry {
	r := &Registry{
		policies: make(map[string]AppPolicy),
	}
	for _, p := range policies {
		r.Register(p)
	}
	return r
}

// Register adds a policy to the registry.
func (r *Registry) Register(p AppPolicy) {
	r.policies[p.ID()] = p
}

// Get returns a policy by ID.
func (r *Registry) Get(id string) (AppPolicy, bool) {
	p, ok := r.policies[id]
	return p, ok
}

// GetAll returns all registered policies.
func (r *Registry) GetAll() []AppPolicy {
	result := make([]AppPolicy, 0, len(r.policies))
	for _, p := range r.policies {
		result = append(result, p)
	}
	return result
}

// List returns all policy IDs.
func (r *Registry) List() []string {
	ids := make([]string, 0, len(r.policies))
	for id := range r.policies {
		ids = append(ids, id)
	}
	return ids
}

// RegistryPolicyStore adapts Registry to implement domain.PolicyStore interface.
type RegistryPolicyStore struct {
	registry *Registry
}

// NewPolicyStore creates a PolicyStore backed by the default Registry.
func NewPolicyStore() domain.PolicyStore {
	return &RegistryPolicyStore{registry: NewRegistry()}
}

func (s *RegistryPolicyStore) GetAll() []domain.Policy {
	policies := s.registry.GetAll()
	result := make([]domain.Policy, len(policies))
	for i, p := range policies {
		result[i] = ToPolicy(p)
	}
	return result
}

func (s *RegistryPolicyStore) GetByID(id string) (*domain.Policy, error) {
	p, ok := s.registry.Get(id)
	if !ok {
		return nil, fmt.Errorf("policy not found: %s", id)
	}
	policy := ToPolicy(p)
	return &policy, nil
}

func (s *RegistryPolicyStore) List() []string {
	return s.registry.List()
}

// Ensure RegistryPolicyStore implements domain.PolicyStore.
var _ domain.PolicyStore = (*RegistryPolicyStore)(nil)
