# Choose your AI provider

In single-user mode, open **Settings → AI provider**. In multi-user mode, only the global administrator configures **Admin → AI provider**. This deployment configuration applies to personal agents, shared group agents and app AI actions; it is not a user setting in multi-user mode. Existing model access restrictions still apply.

## Sources and conversation

1. Select a source to edit it, or click **+ Add a source** and give it a name. Choose **OpenAI**, **Anthropic**, **Ollama** or **Other compatible**. OpenAI and Anthropic have preset official URLs. Ollama requires a server URL. Other compatible covers OpenAI-compatible servers such as vLLM, SGLang and llama.cpp; include `/v1` in the URL.
2. Enter an API key if required. The key is stored encrypted and never returned by the API. Leave it blank to retain an existing key at the same provider and URL. A different URL requires its own credential. Ollama's native connection does not support API keys.
3. **Load models**, then select or enter a model ID. Catalogs do not always identify capabilities; choose a conversation model supporting tools. **Test model** sends a short text request and may incur an API charge. It verifies access, not vision or every tool capability.
4. Set **Default model supports vision** according to the model's capabilities. Vision is needed for image attachments, screenshots and visual widget checks. Text-only models can still chat and use tools. With a saved UI profile, the widget inspection fallback uses the conversation model when vision is enabled. That fallback takes its configuration at server startup; restart after changing it. The embedding model does not interpret images.
5. Choose **Use as default** on the source whose selected model should power new conversations and app actions, then **Save changes**. Other sources remain available in the chat model picker. Conversation changes apply from the next message; a running turn retains its connection.

You can configure several sources of the same provider type, including multiple OpenAI-compatible servers. The picker identifies secondary models by source so identical model IDs cannot silently select the wrong server. A failed model catalog on one source does not hide healthy sources. Enter a model ID manually if it is absent from a catalog.

**Remove source** only removes that connection on saving. Choose another default first if needed, and reassign embeddings if they reference the source being removed. Blank keys are retained only for the same source ID, provider and URL; editing an endpoint never forwards its previous credential to the new host.

On first opening, the form includes the current connection and the other environment-configured connections. Saving adopts this source list without dropping the other connections. Existing single-connection API/tool updates edit the default source and retain the others.

## Embeddings and document search

**Use same provider** follows the default conversation source and reuses its URL and credential, but you must choose a separate embedding model. Existing independent server settings remain independent when opening the form. Anthropic has no embedding endpoint: uncheck the switch and choose another embedding connection. No provider is silently substituted.

When the switch is off, **Embedding source** lets you reuse any configured source, without entering its key again, or choose **Dedicated connection** for a separate provider, URL and key. Reused connections follow changes to that source; a source in use cannot be removed until embeddings are reassigned. **Load models** suggests embedding IDs from the catalog. When capabilities are not published, the list may include other models; you can always enter an ID manually. **Test embeddings** makes a real embedding request and reports its dimension. An empty embedding model disables document search at the next restart.

Embedding changes require a **server restart**. Changing model or endpoint requires checking **Rebuild the document index** before saving. Rebuilding sends all stored chunk text to the selected provider and may incur charges. In multi-user mode this includes the entire deployment index. The index is rebuilt even when the vector dimension is unchanged: different models do not share a vector space.

Prism retains document records and chunk text. Rebuilding runs before document search becomes available, in a database transaction; a failure leaves the old index intact. Check the server log, correct the provider and restart to retry. Stop any other Prism process connected to the same database before changing the index. This mechanism does not coordinate a rolling migration across cloud replicas.

## Server defaults

Without a saved override, `.env` continues to configure Prism, including multiple chat backends, `EMBED_BACKEND`, `EMBED_MODEL` and `VISION_MODEL`. **Use server settings** restores that behavior after confirmation. If the embedding connection changes, it requires a restart and potentially a rebuild. Once indexed, the embedding identity is recorded to prevent accidental mixing of vectors from different models.

## Ask the agent

The existing `agent_settings` tool exposes the same capabilities:

- `ai_get`: read all source IDs/names, the default source and pending embedding restart, without keys.
- `ai_source_set`: add or update one `source_id`, with `source_name`, `provider`, `base_url`, `model`, optional `key_secret` and `make_default`. Pick a short unique ID for a new source.
- `ai_source_default`: select `source_id` and optionally `model` as the default.
- `ai_source_models` / `ai_source_test`: list or test the source identified by `source_id`; an optional `model` tests that model without saving.
- `ai_source_remove`: remove `source_id`, after reassigning the default/embeddings if necessary.
- `ai_models` / `ai_test`: list conversation models or test access.
- `ai_embedding_models` / `ai_embedding_test`: list embedding candidates or test the actual embedding endpoint.
- `ai_set`: save `provider`, `base_url`, `model`, optional `key_secret`, and `chat_vision`.
- Embedding fields: `embedding_use_same_provider`, `embedding_source_id` (empty for a dedicated connection), `embedding_provider`, `embedding_base_url`, `embedding_model`, `embedding_key_secret`. Unspecified fields preserve the existing connection. A chat-only tool update preserves embeddings independently.
- `ai_reset`: restore `.env` defaults.

For embedding changes or resetting to different embedding defaults, obtain the user's agreement to sending indexed text and rebuilding first, then pass `reindex=true`. A restart is an operator action, not performed by this tool. In multi-user mode AI actions require the global admin, and credential names refer to that admin's personal script secrets.

Use the secure Settings/Admin form or `request_secret`, then pass the stored secret **name**. Never put raw keys in chat or tool arguments. A script secret remains accessible to the user's scripts; prefer the AI provider form for a credential intended only for this integration. A headless agent cannot open a secure credential prompt.

Source-qualified model IDs use `source_id::model_id` internally; the UI displays source names. The configured default model retains its plain ID for compatibility. Existing access grants for a plain model ID apply across sources offering that ID; administrators can instead grant a source-qualified ID for a specific connection. User permissions are still checked before model calls.
