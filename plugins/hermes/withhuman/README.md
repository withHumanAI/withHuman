# withHuman for Hermes Agent

Approve or deny Hermes tool calls before they run. [withHuman](https://withhuman.ai) checks each call against your approval rules and asks a person on your team when a rule says to.

## Set up

[Install the withHuman CLI](https://withhuman.ai/docs/web-app/agents), then connect Hermes:

```sh
withhuman agent install hermes
```

Sign in when your browser opens and choose the agent Hermes belongs to. The CLI installs this plugin, enables it and connects it to that agent. Start a new Hermes session afterwards.

## What to expect

Every tool call waits for a decision. Calls your rules approve run at once. A call that needs a person waits until someone approves or denies it, for up to a week.

A denied, expired or failed request blocks the call, and Hermes tells the model why. Stopping the turn withdraws the waiting request.

## Check it works

Ask Hermes to do something your rules send to a person. The request appears in withHuman. Approve it and check the call runs, then try again and deny it.

## Remove it

```sh
withhuman agent eject hermes
```

This removes the plugin and its saved credential. Start a new Hermes session afterwards.

## Limits

- **Hooks that change arguments:** Hermes applies argument changes from hooks that run after this plugin, including shell hooks. Keep other `pre_tool_call` hooks from changing tool arguments, or an approved call can run with different arguments.
- **Hook timeout:** The CLI sets `plugins.hook_callback_timeout` to `0` so Hermes never cuts a long wait short. A lower value blocks any call still waiting when it runs out.
- **Network:** The plugin runs the `withhuman` CLI, which sends each call's tool name and arguments to your withHuman organization.
