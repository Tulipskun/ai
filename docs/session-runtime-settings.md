# Session runtime settings

Model, temperature, thinking level, and API-key selection are session-scoped runtime state.

The application does not require `AI_MODEL`, `AI_TEMPERATURE`, or `AI_THINKING_LEVEL` at startup. A session may start without a model and select one through the session API or a transport-specific command such as `/model`.

Provider credentials remain provider/key-pool scoped. A session may pin a key by index without storing the raw API key in the session.

Request precedence is:

```text
request override → session setting → provider/runtime default
```

Changing a session setting persists it without modifying the canonical conversation history.
