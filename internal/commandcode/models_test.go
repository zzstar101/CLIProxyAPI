package commandcode

import "testing"

func TestPublicModelResolution(t *testing.T) {
	models := []Model{{ID: "zai-org/GLM-5.3"}, {ID: "claude-fixture"}}
	for _, id := range []string{"glm-5.3", "claude-fixture"} {
		m, ok := ResolveModel(models, id)
		if !ok || PublicModelID(m.ID) != id {
			t.Fatalf("cannot resolve %q", id)
		}
	}
	if _, ok := ResolveModel(models, "zai-org/GLM-5.3"); ok {
		t.Fatal("upstream namespace is not a public alias")
	}
	models = append(models, Model{ID: "another/glm-5.3"})
	if _, ok := ResolveModel(models, "glm-5.3"); ok {
		t.Fatal("ambiguous public ID must fail closed")
	}
}
