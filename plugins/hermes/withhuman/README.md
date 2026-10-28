# withHuman for Hermes Agent

Approve or deny Hermes tool calls before they run. [withHuman](https://withhuman.ai) checks each call against your approval rules and asks a person on your team when a rule says to.

## Set up

Install the plugin, then connect it to withHuman:

```sh
hermes plugins install withHumanAI/withHuman/plugins/hermes/withhuman --enable
hermes withhuman setup
```

Setup asks how to connect:

- **Sign in with your browser.** Open the link it prints, sign in and choose the agent Hermes belongs to. This works from a server or container too.
- **Paste an agent key.** In withHuman, open Agents, your agent, New instance, then Generate an API key.

Setup also turns off Hermes's 30 second hook timeout for this profile, so a call can wait for a person. On hosted withHuman it adds the withHuman MCP gateway as an MCP server named `withhuman-gateway`. It offers to install the `withhuman` and `withhuman-pipelines` skills, then sends a test request.

Setup downloads the withHuman CLI the plugin runs, if it isn't installed already.

Until setup has run, the plugin is off and tool calls run without approval.

## What to expect

Calls your rules approve run at once. A call that needs a person waits until someone approves or denies it, for up to a week.

A denied, expired or failed request blocks the call, and Hermes tells the model why. Stopping the turn withdraws the waiting request.

Each Hermes profile has its own connection. Run setup in every profile you use.

## Check it works

Run `hermes withhuman test`, or ask Hermes to do something your rules send to a person. The request appears in withHuman. Approve it and check the call runs.

## Keep it working

Agent keys expire after 90 days by default. When a key expires or is revoked, calls are blocked and Hermes says why. Run `hermes withhuman setup` again to connect a new key.

## Remove it

```sh
hermes plugins remove withhuman
```

Revoke the instance in withHuman if you no longer need it.

## Limits

- **Hooks that change arguments:** Hermes applies argument changes from hooks that run after this plugin, including shell hooks. Keep other `pre_tool_call` hooks from changing tool arguments, or an approved call can run with different arguments.
- **Hook timeout:** Setup sets `plugins.hook_callback_timeout` to `0`. A lower value blocks any call still waiting when it runs out.
- **Network:** The plugin runs the `withhuman` CLI, which sends each call's tool name and arguments to your withHuman organization. When no CLI is installed, or an older one is, setup downloads a fixed CLI version from downloads.withhuman.ai and checks it against a checksum in the plugin before using it. The `withhuman-gateway` MCP server sends your agent key to your withHuman address. The skills come from withhuman.ai through Hermes's own skill installer.
