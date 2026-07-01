# CPM idea 


CPM - claude plugin manager.
convenience way to manage claude code configuration.
Its a TUI program that makes it easear to manage plugins and other resource (hooks, skills, rules, subagents, anythin else?) installed to different claude code profiles. 

You can have different profiles for claude code, for example
- default ~/.claude
- work ~/.claude-work
- personal ~/.claude-personal
- project ~/.claude-myproj

It would be good idea to understand how those profile different between each other except CLAUDE.md. This tool is made for this. It visually draw you a table of all installed plugins group my marketplace.
To get the list of marketplaces for specific claude installation, you can run 
```
CLAUDE_CONFIG_DIR=~/.claude-work claude plugin marketplace list
```

to get the list of plugins:
```
CLAUDE_CONFIG_DIR=~/.claude-work claude plugin list
```


we should draw a table
| ~/.claude | ~/.claude-work | ~/.claude-personal | |
| --------- | -------------- | ------------------ | --- | 
| v: 1.1.0 | v: 1.2.3 | disabled (v1.2.3) | plugin-name@marketplace |

user should be able to update  plugin to the latest version, enable/disable plugin, uninstall plugin, or install if it is installed in other envs, but not in this. 

Program should be written in Go using TUI framework like Bubbletea (i home I write this framework correct, we need to research what is the most advanced TUI framework). 

The same table should be made for MCPs. With the exception, that there is not update action for MCP I think. Need to check. 

For the future plans, not in this task scope:
- the same tab for hooks,
- installed skills
- installed rules
- installed subagents.
