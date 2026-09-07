package executor

import (
	"context"
	"net/http"

	"github.com/tidwall/sjson"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/commandcode"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

// CommandCodeExecutor shares protocol translation and streaming machinery with
// OpenAI compatibility, but owns its credentials, endpoint and account policy.
type CommandCodeExecutor struct{ *OpenAICompatExecutor }

func NewCommandCodeExecutor(cfg *config.Config) *CommandCodeExecutor {
	return &CommandCodeExecutor{NewOpenAICompatExecutor(commandcode.Provider, cfg)}
}
func (e *CommandCodeExecutor) validate(auth *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) error {
	if opts.Alt == "responses/compact" || openAICompatImageEndpointPath(opts) != "" {
		return statusErr{code: http.StatusBadRequest, msg: "command-code: endpoint is not supported"}
	}
	snapshot, ok := commandcode.ReadSnapshot(auth)
	if !ok {
		return statusErr{code: http.StatusServiceUnavailable, msg: "command-code: account discovery is required"}
	}
	modelID := thinking.ParseSuffix(req.Model).ModelName
	if model, unique := commandcode.ResolveModel(snapshot.Models, modelID); unique && commandcode.Enabled(snapshot.Account, model, commandcode.Overrides(auth)) {
		return nil
	}
	return statusErr{code: http.StatusForbidden, msg: "command-code: model is disabled or unavailable for this account"}
}

// commandCodeUpstreamModel runs after translation and payload configuration, so
// client-facing IDs, thinking suffixes and scheduling remain provider-independent.
func (e *OpenAICompatExecutor) commandCodeUpstreamModel(auth *coreauth.Auth, publicID string, payload []byte) ([]byte, error) {
	if auth == nil || auth.Provider != commandcode.Provider {
		return payload, nil
	}
	snapshot, ok := commandcode.ReadSnapshot(auth)
	model, unique := commandcode.ResolveModel(snapshot.Models, publicID)
	if !ok || !unique || !commandcode.Enabled(snapshot.Account, model, commandcode.Overrides(auth)) {
		return nil, statusErr{code: http.StatusForbidden, msg: "command-code: model is disabled or unavailable for this account"}
	}
	return sjson.SetBytes(payload, "model", model.ID)
}

func (e *CommandCodeExecutor) Execute(ctx context.Context, auth *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (coreexecutor.Response, error) {
	if err := e.validate(auth, req, opts); err != nil {
		return coreexecutor.Response{}, err
	}
	return e.OpenAICompatExecutor.Execute(ctx, auth, req, opts)
}
func (e *CommandCodeExecutor) ExecuteStream(ctx context.Context, auth *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	if err := e.validate(auth, req, opts); err != nil {
		return nil, err
	}
	return e.OpenAICompatExecutor.ExecuteStream(ctx, auth, req, opts)
}
