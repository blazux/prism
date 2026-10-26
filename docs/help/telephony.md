# Telephony with PrismVox

When this deployment is connected to PrismVox, open **Admin → Telephony**.
The PrismVox console points to Prism; PrismVox continues to handle SIP, audio,
call control and call archives.

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
