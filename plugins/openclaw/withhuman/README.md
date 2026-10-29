# withHuman for OpenClaw

Human in the loop approvals for OpenClaw. Let OpenClaw work on its own, and have a person approve the actions that matter.

[withHuman](https://withhuman.ai) checks every tool call OpenClaw makes against your approval rules before it runs. Routine calls go straight through. The rest wait for someone on your team to approve or deny them.

You can write rules like:

> Let OpenClaw refund customers up to $20 automatically. Send larger refunds to the support team to approve first.

## What you can do

- **Hold the calls that matter.** Let reads run, and hold emails, deploys, refunds and record changes for a person.
- **Approve from anywhere.** Review requests in the web app, Slack, Discord, email or the withHuman phone app for iPhone or Android.
- **Ask the right people.** Send requests to teams, with escalation levels and working hours.
- **Decide routine calls automatically.** Approve or deny them with your own webhook, or with an AI judge.
- **Give OpenClaw your tools.** OpenClaw uses them through the withHuman MCP gateway, without their credentials.
- **Use one set of rules for every agent.** The same rules cover Claude Code, Codex, Hermes and your own agents.
- **See every tool call.** The audit log records every call OpenClaw makes, and who approved it.

## Set up

You need a withHuman organization. Sign up at [app.withhuman.ai](https://app.withhuman.ai), or [run your own](https://github.com/withHumanAI/withHuman).

```sh
openclaw plugins install clawhub:@withhuman/openclaw
openclaw withhuman setup
```

Setup signs you in through your browser and sends a test request. Approve it in withHuman to check everything works.

Then [set your approval rules](https://withhuman.ai/docs/getting-started/set-up-approval-pipelines).

Until setup has run, the plugin is off and tool calls run without approval.

## Update or remove

```sh
openclaw plugins update withhuman
openclaw plugins uninstall withhuman
```

Agent keys expire after 90 days by default. Run `openclaw withhuman setup` again to connect a new one.

## Security and footprint

- **Sends:** each tool call's name and arguments to your withHuman organization.
- **Stores:** the agent key and the withHuman address in the plugin's settings in `openclaw.json`. On hosted withHuman the gateway's MCP entry there holds the key too.
- **Runs:** the `withhuman` CLI, once for each tool call. It sees only the environment variables it needs, never your provider keys or other plugins' secrets.
- **Downloads:** a pinned version of the withHuman CLI from downloads.withhuman.ai, when it is missing or older. The plugin checks it against a checksum it ships with.
- **Waits:** a tool call can wait up to a week for a decision. The plugin sets this limit on its own hook only. To make calls give up sooner, set `plugins.entries.withhuman.hooks.timeoutMs`; requests then expire just before it.
- **Changes in OpenClaw:** on hosted withHuman setup adds the `withhuman-gateway` MCP server, which sends the agent key to your withHuman address. It offers to install the `withhuman` and `withhuman-pipelines` skills from ClawHub.
- **Left after removal:** the gateway and the skills. Remove them with `openclaw mcp unset withhuman-gateway` and `clawhub --workdir ~/.openclaw uninstall <name>`.
