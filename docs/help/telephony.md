# Telephony with PrismVox

PrismVox connects to both personal and multi-user Prism deployments. Open the
**Telephony** app from the app list. **Calls** shows call traffic, history and tool results, with automatic refresh.
**Outbound calls** lets you schedule calls and track, cancel or retry your own
missions. Use the normal Prism agent to ask about telephone activity.
The owner configures the line, agents and speech in **Settings → Telephony**
in personal mode; global administrators use **Admin → Telephony** in shared mode.
The PrismVox console points to this app in Prism. PrismVox continues to handle
SIP, audio, call control and call archives.

## Connect and configure PrismVox

Open **Settings → Telephony** in personal mode or **Admin → Telephony** in
shared mode. Configuration belongs to the installation, independently of the
workspace currently open. Group administrators and members with app access do
not gain configuration privileges.

Under **Connection**, enter the PrismVox API address and its username/password,
then the Prism address that PrismVox can reach. Docker service names are suitable
only when both services share the relevant Docker network. The optional public
Prism address is the URL used by browsers. **Test connection** checks access;
**Save & connect** saves the link and configures its reverse connection. Check
the separate reachability and authentication indicators in both directions.
Passwords and the gateway credential remain server-side. Existing environment
settings remain defaults until a connection is saved from this screen.

**Agent & voice** contains the models, inbound and outbound instructions, voice,
greetings and speech providers. **Access & resources** contains group app access,
public knowledge and external tools. **Advanced** contains specialist model,
embedding, pronunciation, phrase and call-handling settings. Each save button
names the form it writes; switching sections preserves unsaved drafts. Saved
agent settings apply to new calls.

## Ask the Prism agent about calls

Ask in the normal Prism chat: “How many outgoing calls this week?”, “List the
calls from this number”, or “Which subject comes up most often?”. In the Room,
mention the shared agent. It uses the same conversation, tools and context
controls as for other work. There is no fixed selection of recent calls.

The Telephony app's question button prepares a draft in your personal assistant
or the selected group's Room, with the current call or filters. Review and send
that draft. The app keeps call traffic, transcripts, tool results and scheduling
separate from configuration. **Earlier conversations** retains the old Vox
supervision exchanges in read-only form when connected; new questions go to
Prism's agent. Standalone Vox keeps its own supervision chat.

The `call_history` tool reads the archive with the requesting identity's rights.
It accepts dates, number, direction and text filters; `total` is the exact
matching call count. Follow `next_offset` for more summaries, and use the
returned call IDs to read transcripts or tool results. Content analysis should
state the period and records actually read. See **prism_help(topic="call-history")**
for all parameters, pagination and empty-result handling. The existing scoped
HTTP API remains available to server-side scripts; native reads do not need shell
access. `list_calls` reads the outbound mission queue instead.

A Room always uses the group's rights, including when the person asking is a
global administrator. Enable existing groups under **Telephony → Access & resources
→ App access**. Each authorized group can consult the standard's calls;
recognized callers' personal conversations are excluded. Personal and outbound
calls retain their owner and installation identity. **Administrator archive** is
a separate private view for global administrators, including older calls whose
audience is unknown. Those records are not automatically exposed to groups or
the personal agent's archive API.

In personal mode the app includes the standard's calls and the owner's recognized
calls. Fill **Settings → Profile → Phone** so calls from that number reach the
personal assistant. It can use the owner's existing notes, calendar, tasks,
configured mailbox and permitted phone tools. Other callers reach the public
receptionist. Caller-number recognition is not strong authentication.

## Outbound calls

Open **Telephony → Outbound calls**, enter the phone number and the agent’s mission,
then choose a future date and time. The displayed timezone is the browser’s timezone.
Check **Call as soon as possible** for an immediate mission. **Schedule call** adds
the mission to your account’s queue. You can cancel pending calls or retry a finished
mission immediately. Selecting another reception archive group does not change the
owner of your outbound calls.

You can also ask the agent to call now or at a future time. `place_call` takes a
self-contained mission, a number or outbound contact name, optional `scheduled_at`
with timezone, and `max_attempts` from 1 to 10 (default 3). It returns a mission
ID. `list_calls(call_id=...)` reads that mission even when it is no longer on the
first page. Without an ID, use its `next_offset` to continue through the queue.
`cancel_call` cancels a still-pending mission; it cannot stop a call already live.

When a mission created by the Prism agent is followed successfully, its result is
saved once in the originating conversation and notifications. This tracking
survives Prism restarts and resumes when the same Vox connection is available.
If that conversation is still processing another request, the result is posted
after its current turn finishes.
Missions requested on the phone report back to that caller's personal Prism
assistant. A Room receives its result in the same Room. A messaging-channel reply
is attempted only to the requesting user's linked chat or an exact saved original
conversation; an unavailable channel never falls back to another group's
announcement destination. The result remains saved in Prism if that delivery
fails. Calls already placed still need a confirmed summary before the agent can
claim that their mission succeeded.

If the creation result says automatic tracking is unavailable, keep its mission
ID and use `list_calls` to check it. Do not originate another call just because
tracking failed or a network response was lost.

## Agents & models

- **Receptionist and outbound model** can follow the default Prism model or use
  a model from one of Prism's configured sources. Prism routes these inference
  requests without copying the source's provider key into Vox. Reception,
  outbound missions, transfer recipients, summaries and Vox text operations use
  that selection. **Keep the Vox deployment model** retains the independently
  configured Vox gateway and model instead.
- **Conversation model — recognised callers** selects the Prism model for calls
  from recognized profiles. Its default follows the deployment's AI model;
  changing it does not alter the web chat's model.
- **Switchboard — unknown callers** defines the public receptionist's instructions.
  **Outbound instructions** provides a separate role for outbound missions. These
  two roles can use the same model without sharing a conversation or personal data.
- The knowledge selector under **Access & resources** chooses the single collection
  available to unknown callers. Only switchboard collections and collections of
  existing groups can be selected. Personal collections are not eligible. The
  global administrator must explicitly choose any group collection before its
  contents can be read to external callers.

Model-source credentials, personal account permissions and phone tools remain
separate. Selecting a Prism-hosted model for the public receptionist gives it
model inference, not the personal assistant's email, files or permissions.

Recognized callers can use their personal notes, tasks, calendar and connected
mailbox through the same tools as in Prism. Their account permissions and hidden
tool preferences apply to phone calls too. They also receive their profile,
personal memory and permitted knowledge. Configure accounts and mailbox rules
from Settings; shell, server administration, secret management, MCP and custom
tools are unavailable by phone. Recognized callers can read their permitted call
archive and use outbound tools when their account's tool policy allows them.
Unknown callers continue to converse with Vox.
Outbound missions also use Vox when connected.

## Voice & greeting

New PrismVox installations greet callers in English. Open **Speech providers**
to choose the default language and **Adapt to the caller**. The agent follows
the conversation and explicit requests to switch language. Use a multilingual
recognition and speech model. The setting does not change account permissions.

**Preload fallback languages** and **Prepare fallback translations** prepare
saved silence prompts and call-control templates before calls. Customized phrases use
the configured Vox model; standard English and French fallbacks are built in.
Audio is generated on demand and cached. **Call summary language** selects the
operator’s summary language; blank uses the default language. Existing saved
personas, greetings and phrases are preserved on upgrade.

Choose the voice and the separate greetings for unknown and recognized callers,
then **Save voice & greeting**. Keep `%s` in a recognized-caller greeting to insert
the caller's name. Outbound openings are generated from the mission.

**Speech providers** has its own save button. **Connect** loads provider choices;
it does not save them. Spoken phrases and pronunciation have separate Save
buttons. Preserve the `%s` placeholders used for names and transfer reasons.

## Transfers & directories

The transfer directory uses approved Prism profiles with a phone number.
**Blind** transfers directly; **Attended** announces the caller and asks the
recipient to accept. The outbound directory contains contacts the agent can
call, such as suppliers; these entries are not inbound transfer destinations.

**Call handling** sets the maximum number of exchanges with a transfer recipient
while the caller waits. Save this block separately.

## Phone connection

Configure the SIP trunk and use **Save & apply** to save and reload it. Leaving
the password blank preserves the saved password. Saved agent settings apply to
new calls; calls already in progress keep their configuration.

## Standalone PrismVox

Without Prism, open **Settings** in the Vox console. Its four tabs are
**Agents & models**, **Voice & greeting**, **Transfers & directory** and
**Phone connection**. Every inbound caller uses the Vox inbound agent; personal
Prism recognition and memory require the connection to Prism.

**Save changes** saves only its form. **Discard changes** reloads that form's
saved values. Switching tabs preserves drafts; reloading the page discards
unsaved edits. The standalone voice selector saves immediately. Pronunciation
has its own **Save** and **Cancel** buttons. For SIP, **Save** stores the
settings, while **Apply** saves them and reloads the trunk.
