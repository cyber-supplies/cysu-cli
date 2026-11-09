Profiles allow a user to convert their methodology into repeatable LLM workflows that can be later instantiated on any machine. They consist of Skills, Prompts, MCP Servers, and Agents.

To list the current profiles:

```
cysu profiles list     

[
  {
    "id": "example",
    "tag": [
      "example-tag",
      "1"
    ]
  }
]
```

Profiles are edited by pulling the files onto the local machine, then pushing the changes/additions to Cyber Supplies. To pull a specific profile or all profiles:

```
cysu profiles edit [--profile <id>] [--all]

// --profile

{
  "directory": "/home/jakubjn/cysu/profiles/example",
  "items": 11,
  "profile": "example",
  "success": true
}

// --all

{
  "profiles": [
    {
      "directory": "/home/jakubjn/cysu/profiles/example",
      "items": 11,
      "profile": "example",
      "success": true
    }
  ],
  "success": true
}

```

**Important** - This pulls profiles into ~/cysu/profiles and replaces local changes in each downloaded profile. '--all' downloads every remote profile; unrelated local profile folders are left untouched.

The directory ~/cysu/profiles expects the following structure:

```
/home/jakubjn/cysu/profiles
└── <profile-name>
    ├── agents
    │   └── <agent-name>
    │       └── AGENT.md
    ├── mcp
    │   └── <mcp-name>
    │       └── settings.json
    ├── prompts
    │   └── <prompt-name>
    │       └── AGENTS.md
    ├── skills
    │   └── <skill-name>
    │       └── SKILL.md
    └── tags.txt // New-line delimited tags
```

To save a profile after edits, or to save all profiles currently stored locally:

```
cysu profiles save [--profile <id>] [--all]

// --profile

{
  "changes": {
    "created": 0,
    "deleted": 0,
    "unchanged": 11,
    "updated": 0
  },
  "directory": "/home/jakubjn/cysu/profiles/example",
  "profile": "example",
  "success": true
}

// --all

{
  "profiles": [
    {
      "changes": {
        "created": 0,
        "deleted": 0,
        "unchanged": 11,
        "updated": 0
      },
      "directory": "/home/jakubjn/cysu/profiles/example",
      "profile": "example",
      "success": true
    }
  ],
  "success": true
}
```

**Important** - Saving replaces each selected profile's contents, including deleting items removed locally from that profile. '--all' saves every local profile; remote profiles without a local folder are left untouched.

To create a new profile, optionally with example data filled in:

```
cysu profiles create --profile <id> [--example]

{
  "directory": "/home/jakubjn/cysu/profiles/example",
  "items": 4,
  "profile": "example",
  "success": true
}
```

The profile will not be created in Cyber Supplies until it is saved.
