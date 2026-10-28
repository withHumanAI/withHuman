# withHuman for Hermes Agent

Human in the loop approvals for Hermes Agent. Let Hermes work on its own, and have a person approve the actions that matter.

[withHuman](https://withhuman.ai) checks every tool call Hermes makes against your approval rules before it runs. Routine calls go straight through. The rest wait for someone on your team to approve or deny them.

You can write rules like:

> Let Hermes refund customers up to $20 automatically. Send larger refunds to the support team to approve first.

## What you can do

- **Hold the calls that matter.** Let reads run, and hold emails, deploys, refunds and record changes for a person.
- **Approve from anywhere.** Review requests in the web app, Slack, Discord, email or the withHuman phone app for iPhone or Android.
- **Ask the right people.** Send requests to teams, with escalation levels and working hours.
- **Decide routine calls automatically.** Approve or deny them with your own webhook, or with an AI judge.
- **Give Hermes your tools.** Hermes uses them through the withHuman MCP gateway, without their credentials.
- **Use one set of rules for every agent.** The same rules cover Claude Code, Codex, OpenClaw and your own agents.
- **See every tool call.** The audit log records every call Hermes makes, and who approved it.

## Set up

You need a withHuman organization. Sign up at [app.withhuman.ai](https://app.withhuman.ai), or [run your own](https://github.com/withHumanAI/withHuman).

```sh
hermes plugins install withhuman --enable
hermes withhuman setup
```

Setup signs you in through your browser and sends a test request. Approve it in withHuman to check everything works.

Then [set your approval rules](https://withhuman.ai/docs/getting-started/set-up-approval-pipelines).

Until setup has run, the plugin is off and tool calls run without approval.

## Update or remove

```sh
hermes plugins update withhuman
hermes plugins remove withhuman
```

Agent keys expire after 90 days by default. Run `hermes withhuman setup` again to connect a new one.

## Security and footprint

- **Sends:** each tool call's name and arguments to your withHuman organization.
- **Stores:** the agent key in this profile's secrets (`WITHHUMAN_AGENT_TOKEN`), and the withHuman address in the plugin's settings.
- **Runs:** the `withhuman` CLI, once for each tool call.
- **Downloads:** a pinned version of the withHuman CLI from downloads.withhuman.ai, when it is missing or older. The plugin checks it against a checksum it ships with.
- **Changes in Hermes:** setup sets `plugins.hook_callback_timeout` to `0`, so a call can wait for a person. On hosted withHuman it adds the `withhuman-gateway` MCP server, which sends the agent key to your withHuman address. It offers to install the `withhuman` and `withhuman-pipelines` skills from withhuman.ai.
- **Left after removal:** the timeout setting, the gateway and the skills. Remove them with `hermes config set plugins.hook_callback_timeout 30`, `hermes mcp remove withhuman-gateway` and `hermes skills uninstall <name>`.
- **Other hooks:** Hermes applies argument changes from hooks that run after this one. Keep other `pre_tool_call` hooks from changing arguments, or an approved call can run with different arguments.
