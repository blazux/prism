# Choose your AI provider

In single-user mode, open **Settings → AI provider**. In multi-user mode, only the global administrator configures **Admin → AI provider**. This deployment configuration applies to personal agents, shared group agents and app AI actions; it is not a user setting in multi-user mode. Existing model access restrictions still apply.

## Sources and conversation

1. Select a connection in the list, or click **Add connection** and give it a name. Choose **OpenAI**, **Anthropic**, **Ollama** or **Other compatible**. OpenAI and Anthropic use their official addresses by default. Ollama requires a server URL. For an OpenAI-compatible server such as vLLM, SGLang or llama.cpp, include `/v1` in its URL. Open the connection's advanced options to use a custom OpenAI or Anthropic endpoint; an existing custom address remains visible.
2. Enter the provider's API key if required. Prism stores keys encrypted. A saved key is shown as **Saved securely** without displaying its value. Use **Replace** to enter a different key or **Remove** to mark it for deletion. These actions take effect only when you save. Changing the provider or address requires a credential for that destination; Prism never forwards a saved key to a different endpoint. Ollama's native connection does not use API keys.
3. Click **Verify connection** to load the available models. This checks access without saving or generating a model response. Enter a model ID directly or choose a suggestion from the list. Catalogs do not always identify capabilities; choose a conversation model that supports tools.
4. Choose **Use as default** on the connection whose selected model should power new conversations and app actions. Other connections remain available in the chat model picker.
5. Click **Save changes** to apply the edited connections, selected default, model settings and document-search options. Wait for the saved confirmation before leaving. Changing a field or choosing a model does not save automatically.

For example, to use Claude for conversation without document search, choose **Anthropic**, paste a console API key, verify the connection and choose a returned model. Leave **Document search → Connection** set to **Disabled**, then save.

**Model settings** opens the settings for one model on the selected connection. You can enter another model ID from that connection without changing its selected model. **Context window** defaults to Automatic: Prism uses explicit provider metadata when available, otherwise a conservative budget. Enter the effective server context in tokens (4096–2000000) if the provider does not report it. For Ollama, a manual value also sets `num_ctx` on requests for that model. **Vision support** defaults to provider metadata or the existing deployment setting; choose Supports images or Text only to override it. Vision is needed for image attachments and visual widget checks; embeddings do not interpret images.

Click **Apply to changes** to keep the model settings in the form, then **Save changes** on the main page to apply them. **Reset to automatic** resets the dialog's fields; it also requires applying and saving. These settings never select a different default or alter embeddings. The optional **Test model** action sends a short text request and may incur a provider charge; it verifies text access, not every tool or vision capability.

You can configure several connections of the same provider type, including multiple OpenAI-compatible servers. The picker identifies secondary models by source so identical model IDs cannot silently select the wrong server. A failed model catalog on one source does not hide healthy sources. Enter an ID manually if it is absent from a catalog.

On first opening, the form includes the current connection and the other environment-configured connections. Saving adopts this source list without dropping the other connections. Existing single-connection API/tool updates edit the default source and retain the others. Connection changes apply from the next message; a running turn retains its connection.

## Phone conversations

When PrismVox is connected, a global administrator can choose a separate
model under **Admin → Telephony → Agents & models** in shared mode, or
**Settings → Telephony → Agents & models** in personal mode, using
**Conversation model — recognised callers**.
Configure its source in **AI provider** first, select the phone model, then click
**Save model**. The setting applies to new calls and does not change the web
model, embeddings, caller profile, memory, knowledge scope or permitted tools.
Choose **Default AI model** to follow the deployment default again. Unknown
callers use **PrismVox model — unknown callers & outbound calls** in that same
**Agents & models** section.

## Embeddings and document search

Expand **Document search** to configure an optional embedding model. Choose **Disabled** to turn document search off, **Use default conversation connection** to reuse the default conversation provider, **Use another connection** to reuse a different configured connection, or **Dedicated embedding connection** to enter a separate provider, URL and key. Reusing a connection also reuses its credential. Choose an embedding model separately from the conversation model. Anthropic has no embedding endpoint, so select another connection for document search.

**Load models** loads embedding-model suggestions without saving. Some providers do not publish model capabilities, so the suggestions may include conversation models; you can enter the embedding ID directly. **Test embedding model** makes a real embedding request, reports its dimension and may incur a charge.

Click **Save changes** to apply document-search edits. Changing the model or endpoint requires checking **Rebuild the document index** before saving. Rebuilding sends all stored chunk text to the selected provider and may incur charges. In multi-user mode this includes the entire deployment index. The index is rebuilt even when the vector dimension is unchanged: different models do not share a vector space. Disabling document search retains stored documents and chunks.

Embedding changes apply without restarting the server, and the page displays progress. Rebuilding happens in a database transaction before document search becomes available; a failure leaves the old index intact. Check the error, correct the connection and save again to retry. Stop any other Prism process connected to the same database before changing the index. This mechanism does not coordinate a rolling migration across cloud replicas.

## Server defaults

Without a saved override, `.env` continues to configure Prism, including multiple chat backends, `EMBED_BACKEND`, `EMBED_MODEL` and `VISION_MODEL`. **Restore server defaults** marks the form to restore those defaults after confirmation. Click **Save changes** to apply the reset, or **Discard changes** to keep the active configuration. If the embedding connection changes when the reset is saved, the document index may be rebuilt. Once indexed, the embedding identity is recorded to prevent accidental mixing of vectors from different models.

Hosted personal environments such as Prism Cloud do not provide server AI defaults: the reset button is hidden and `ai_reset` is unavailable. Check `serverDefaultsAvailable` in `ai_get` before suggesting a reset; configure a provider instead when it is false. Local installations retain the reset option.

## Ask the agent

The existing `agent_settings` tool exposes the same capabilities:

- `ai_get`: read all source IDs/names, the default source and embedding application status, without keys.
- `ai_source_set`: add or update one `source_id`, with `source_name`, `provider`, `base_url`, `model`, optional `key_secret` and `make_default`. Pick a short unique ID for a new source.
- `ai_source_default`: select `source_id` and optionally `model` as the default.
- `ai_source_models` / `ai_source_test`: list or test the source identified by `source_id`; an optional `model` tests that model without saving.
- `ai_source_remove`: remove `source_id`, after reassigning the default/embeddings if necessary.
- `ai_models` / `ai_test`: list conversation models or test access.
- `ai_embedding_models` / `ai_embedding_test`: list embedding candidates or test the actual embedding endpoint.
- `ai_set`: save `provider`, `base_url`, `model`, optional `key_secret`, and `chat_vision`.
- Embedding fields: `embedding_use_same_provider`, `embedding_source_id` (empty for a dedicated connection), `embedding_provider`, `embedding_base_url`, `embedding_model`, `embedding_key_secret`. Unspecified fields preserve the existing connection. A chat-only tool update preserves embeddings independently.
- `ai_reset`: restore `.env` defaults.

For embedding changes or resetting to different embedding defaults, obtain the user's agreement to sending indexed text and rebuilding first, then pass `reindex=true`. The tool applies changes automatically; use `ai_get` to check progress. In multi-user mode AI actions require the global admin, and credential names refer to that admin's personal script secrets.

Use the secure Settings/Admin form or `request_secret`, then pass the stored secret **name**. Never put raw keys in chat or tool arguments. A script secret remains accessible to the user's scripts; prefer the AI provider form for a credential intended only for this integration. A headless agent cannot open a secure credential prompt.

Source-qualified model IDs use `source_id::model_id` internally; the UI displays source names. The configured default model retains its plain ID for compatibility. Existing access grants for a plain model ID apply across sources offering that ID; administrators can instead grant a source-qualified ID for a specific connection. User permissions are still checked before model calls.

## Saving and removing sources

The saved configuration remains active while you edit. **Save changes** applies all pending changes together. Checking a connection, selecting a model, changing the default, editing model limits or removing a connection only changes the form until you save. Errors remain visible and leaving with unsaved changes prompts a warning.

**Remove connection** removes the connection from the form. Removing the default selects another connection in the form. If the connection is used for document search, choose another embedding connection or disable document search before removing it. The last connection cannot be removed; edit it or restore server settings instead. A failed save leaves the active configuration intact. Removing a saved key likewise takes effect only after saving; use **Replace** to enter a different key.

## Conversation context and compaction

The compact line beneath the model selector displays the context window and vision support. Explicit provider metadata is marked **auto-detected**; overrides are marked **manual**. Missing metadata is labelled **not detected** with the fallback indicated. Discovery runs in the background without changing settings or generating a model response.

For an OpenAI-compatible gateway, Prism reads `/models` and, when metadata is incomplete, tries LiteLLM's `/model_group/info` on that same gateway. Only capabilities reported for the exact selected model are used. A proxy may list Qwen yet report no context limit: in that case, enter the effective context configured on its inference server in **Model settings**, or ask the proxy administrator to publish that limit. Prism does not infer a context window from a model name or connect directly to the gateway's upstream servers.

LiteLLM's group endpoint replaces unknown vision support with `false`. Prism treats that ambiguous negative value as **not detected**, preserving the existing vision setting. A positive group value, explicit `/models` vision metadata, or a manual **Supports images** / **Text only** setting is still respected.

Context settings belong to a source and model together: two servers exposing the same model ID can have different limits. A small model on an unused source does not constrain the selected model. Prism reserves room for generation, counts system instructions, tools and history, and calibrates its estimates from provider input-token usage when supplied. Estimates are not an exact tokenizer, especially for images. Live compaction summarizes older completed turns near the context limit and retains the current working turn. A failed summary retains the original exchanges. Compaction preserves the visible stored transcript and its persisted summary boundary across reconnections; the old 40-message deletion rule is no longer used. Older details already deleted by a previous version cannot be recovered.

In **Model settings → Generation limit (tokens)**, set the maximum generated tokens for each call in the agent’s conversation. This includes reasoning, the reply and tool arguments. Leave it blank to use the default of **16,384**. Use a value supported by the model and its inference server. For example, a model with a 500,000-token context can use a 65,536-token generation limit. The setting belongs to that source and model and applies to the next message using it.

Prism limits generation to one quarter of a known context window and reserves that same amount before compacting history. With a 32,768-token context, a requested generation limit of 65,536 becomes 8,192. A larger limit gives the model more room; it does not make every response that long or guarantee that a task will finish. The limit applies to each call in the agent’s conversation, separately from its iteration limit. Internal summaries keep their own smaller generation budget.

`LIVE_CONTEXT_CHAR_BUDGET`, if explicitly configured, remains a deployment ceiling. Without it, a known model uses its own context window. Chat channels may retain their existing tighter latency budget. No provider completion is made to discover metadata; **Test model** remains a separate potentially billable operation.

The existing `agent_settings` tool can inspect or adjust these settings with `ai_model_get` / `ai_model_set`, `source_id` and `model`. For writes, `context_window=0` restores Automatic; `max_output_tokens=0` restores the default generation limit; `model_vision` is `auto`, `yes` or `no`. Omitted fields are retained. The same mono-user/global-administrator permissions apply.

## Claude caching and tool discovery

Native **Anthropic** sources use prompt caching automatically. Prism places cache
boundaries around stable instructions and tool definitions; the current session
and other changing context remain after the shared instruction prefix. Cached
input still counts toward the model's context window. Cache hits depend on the
provider's minimum prefix length, identical content and expiry; they are not
promised on every request. These options do not apply to an OpenAI-compatible
source merely because its model has a Claude name.

The default cache lifetime is **5 minutes**. To request **1 hour**, set
`PRISM_ANTHROPIC_CACHE_TTL=1h` in the deployment environment and recreate the Prism
server. Use `5m` or remove the variable to return to the default. Unsupported
values use the default. One-hour writes cost twice the ordinary input rate;
five-minute writes cost 1.25 times that rate. A longer lifetime is useful only
when enough later requests reuse the cached prefix. Provider-managed search
results may still have a five-minute lifetime.

Two experimental tool-discovery modes are available, both disabled by default:

- `PRISM_ANTHROPIC_TOOL_SEARCH=1` enables Anthropic's native search for compatible
  Claude models when at least eight tools are available. Core file, command,
  widget and scheduling tools remain immediately visible; other permitted tools
  are discoverable by the model. The native mode currently receives complete
  responses rather than displaying them token by token. Unsupported models keep
  the full catalog.
- `PRISM_CLAUDE_LAZY_TOOLS=1` enables Prism's client catalog for native Anthropic
  conversations. The agent starts with its core tools and `tool_catalog`, which
  lists and loads the remaining permitted tools for the current turn. If both
  flags are set, this catalog takes priority where it is actually active.

These settings do not grant new permissions. Native search exchanges are kept
with the conversation using a restricted format that excludes hidden reasoning
and reuses the existing redacted client-tool arguments. If search is disabled,
referenced tools are removed, or stored metadata cannot be restored, Prism uses
its ordinary conversation representation. Older history without search metadata
also uses ordinary replay.

For a useful comparison, run the same complete tasks with the same model and
prompt profile, first with the full catalog, then each discovery mode separately.
Compare task success, tool errors, extra model calls, latency and total reported
cost. Measure fresh conversations and later messages separately before enabling a
mode for regular use. See the repository's `eval/README.md` for the evaluator.

Provider references: [prompt caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching)
and [tool search](https://platform.claude.com/docs/en/agents-and-tools/tool-use/tool-search-tool).

## OpenAI caching and tool discovery

Official **OpenAI** connections use the provider's automatic prompt cache. Prism
keeps the shared instruction prefix stable and supplies an opaque routing key on
earlier supported GPT models. Changing workspace does not by itself create a new
routing key. The key contains neither the API credential nor instruction text.
Cache hits depend on the actual prefix, model, request settings and expiry.

Leave `PRISM_OPENAI_CACHE_RETENTION` unset to use the provider's default. Set it to
`24h` to request extended retention on supported models, then recreate the Prism
server. Unsupported models keep their provider default. This option changes
provider-side cache retention; consult your OpenAI account's data-retention
requirements before enabling it. It does not add persistent conversation storage
to Prism. Model support and cache pricing vary by generation.

`PRISM_OPENAI_LAZY_TOOLS=1` enables the experimental Prism tool catalog for
supported GPT conversations on `https://api.openai.com/v1`. It is disabled by
default. The agent starts with its permitted core tools and `tool_catalog`; it
can load all other permitted tools for the current turn. Discovery can add a
model call, so compare the complete task cost. Tool permissions remain in force.
These settings do not apply to compatible gateways or local models with GPT-like
names.

Prism's OpenAI connection currently uses Chat Completions. OpenAI's hosted tool
search requires the Responses API and is not enabled by the Prism catalog flag.
Models that require Responses for function calling cannot use Prism tools through
this connection.

Provider references: [prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching),
[tool search](https://developers.openai.com/api/docs/guides/tools-tool-search)
and [reasoning models](https://developers.openai.com/api/docs/guides/reasoning).

## Usage and estimated cost

**Admin → Usage** shows provider-reported input, output, cache reads, and cache
writes split by lifetime, with estimated standard API prices for supported
models. Unknown models, missing counters and unknown write lifetimes stay
unpriced. The trace includes conversation calls, delegated agents, compaction,
deep research and app AI assistance. It excludes embeddings, auxiliary image
captions, connection tests and the phone inference gateway; it is not a complete
invoice. A proxy's negotiated prices may differ from these estimates.

GPT estimates require a supported model, complete token counters and an official
OpenAI response at the standard service tier. Missing provenance, unsupported
pricing, and priority or other service tiers stay unpriced. Cached input and
reasoning tokens are already included in total input and output respectively;
they are not added a second time. Long-context rates are applied per model
request before totals are combined. See [OpenAI pricing](https://developers.openai.com/api/docs/pricing)
for current rates.
