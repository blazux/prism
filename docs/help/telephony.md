# Telephony with PrismVox

PrismVox connects to both personal and multi-user Prism deployments. Open the
**Telephony** app from the app list. **Calls** shows call traffic, history and tool results, with automatic refresh.
**Outbound calls** lets you schedule calls and track, cancel or retry your own
missions. **Supervision** lets you ask the reception agent about its work.
The owner configures the line, agents and speech in **Settings → Telephony**
in personal mode; global administrators use **Admin → Telephony** in shared mode.
The PrismVox console points to this app in Prism. PrismVox continues to handle
SIP, audio, call control and call archives.

## Supervision and call history

For an operational archive question, read **prism_help(topic="call-history")**.
That guide gives the scoped request, supported parameters, pagination and the
meaning of refusals or empty results. Use the current tools and access facts;
`list_calls` only reads the outbound-mission queue.

Ask the reception agent directly: “How many outbound calls this week?”, “List
all calls from this number”, or “Which subject comes up most often?”. The agent
chooses which archived records to read. There is no fixed recent-call selection.
Counts cover the matching records; content analysis reads paginated summaries and
can request transcripts or tool results. If pages remain unread, the agent must
state that coverage is incomplete.

The supervision conversation, replies and tool traces are saved. **Earlier
messages** retrieves older exchanges. **Agent context** shows the retained
summary and recent active span; **Lighten context** summarizes older exchanges
without deleting the transcript. Call-history filters apply to the journal,
independently of the question asked in supervision.

In a Room, archive access follows the group's permissions, including when the
person asking is a global administrator. `list_calls` reads the outbound-task
queue; it is not the incoming-call or transcript archive. The shared agent uses
the scoped archive API through its existing execution tool. That tool must also
be allowed by the applicable tool policy.

In multi-user mode a global administrator opens **Admin → Telephony → App access** and
selects the existing groups whose members may use it. Each group has its own
supervision conversation and can consult the standard's calls. Recognized
callers' personal conversations are excluded. Personal and outbound calls retain
their owner and installation identity. **Administrator archive** is a separate
private conversation for global administrators, including older calls whose
audience is unknown. Such older records are not automatically exposed to groups.

In personal mode the app belongs to the owner and includes the standard's
calls and their own recognized calls. Fill **Settings → Profile → Phone** so that
calling from that number reaches the personal assistant. It uses the owner's
existing notes, calendar, tasks and configured mailbox; no additional user account
or shared deployment is required. Other callers use the standard. Caller-number
recognition is not strong authentication.

## Outbound calls

Open **Telephony → Outbound calls**, enter the phone number and the agent’s mission,
then choose a future date and time. The displayed timezone is the browser’s timezone.
Check **Call as soon as possible** for an immediate mission. **Schedule call** adds
the mission to your account’s queue. You can cancel pending calls or retry a finished
mission immediately. Selecting another reception archive group does not change the
owner of your outbound calls.

## Agents & models

- **Conversation model — recognised callers** selects a configured Prism model.
  **Default AI model** follows the deployment default. Changing this selection
  does not change the web agent's model.
- **Switchboard — unknown callers** contains the inbound instructions and the
  public knowledge collection. Documents in that collection can be read to
  unknown callers. The collection selector saves immediately; the personality
  has its own **Save switchboard** button.
- **PrismVox model — unknown callers & outbound calls** selects the shared Vox
  model for unknown callers, outbound missions, transfer recipients, summaries
  and console chats. Use **Save PrismVox model** after editing it.
- **Outbound instructions** defines the role for outbound missions. Use
  **Save outbound instructions** after editing this block. Incoming switchboard
  instructions remain separate; both use the same Vox model.

Vox model IDs belong to its configured API, independently of Prism's model
catalogue. Optional model endpoints are under **Advanced: model endpoint**.
An empty model field uses the Vox deployment default; an empty endpoint uses
its deployment gateway. Different endpoints do not receive the gateway key.

Recognized callers can use their personal notes, tasks, calendar and connected
mailbox through the same tools as in Prism. Their account permissions and hidden
tool preferences apply to phone calls too. They also receive their profile,
personal memory and permitted knowledge. Configure accounts and mailbox rules
from Settings; shell, server administration, secret management, MCP and custom
tools are unavailable by phone. Unknown callers continue to converse with Vox.
Outbound missions also use Vox when connected.

## Voice & greeting

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
