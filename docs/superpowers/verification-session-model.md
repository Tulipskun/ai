Session model startup invariant:

- `AI_MODEL` must not be required by `cmd/ai`.
- The runtime base session config leaves `Model` empty.
- `RouterClient.Generate` resolves an empty request model from `Session.Config().Model`.
- A transport can set the model through the session API before the first model request.
