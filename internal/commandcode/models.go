package commandcode

import "strings"

// PublicModelID removes only the vendor namespace; persisted policy keys remain upstream IDs.
func PublicModelID(id string) string {
	_, leaf, found := strings.Cut(id, "/")
	if found {
		return strings.ToLower(leaf)
	}
	return strings.ToLower(id)
}

// ResolveModel fails closed on collisions, regardless of either model's current eligibility.
func ResolveModel(models []Model, publicID string) (Model, bool) {
	var match Model
	count := 0
	for _, model := range models {
		if PublicModelID(model.ID) == publicID {
			match = model
			count++
		}
	}
	return match, count == 1
}
