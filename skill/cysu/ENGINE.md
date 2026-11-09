The engine allows running multi-profile workflows, where each profile runs inside of an isolated containe with cysu and security tools attached. 

The engine is hosted on the user's infrastructure and must be configured. To check if the engine is configured and successfully configured:

```
cysu engine status

{
  "version": "0.2.0",
  "docker": "29.9.3",
  "image": "ghcr.io/cyber-supplies/cysu-engine-workspace:0.2.0",
  "apiUrl": "https://cysu-gateway-670100724771.europe-west1.run.app",
  "cysu": true,
  "llm": {
    "mode": "codex",
    "ready": true
  },
  "configured": true
}
```

If configured is false, report that setup is incomplete. Existing workflows and runs can still be inspected or managed, but starting a run requires completed authentication. Set CYSU_ENGINE_URL and use 'cysu engine auth --passphrase <passphrase>' to complete setup. Surface connection or authentication errors immediately rather than trying to bypass them.

## Workflows

To list the current workflows in the engine:

```
cysu engine list-workflows

[
  {
    "id": "8e4759b1-9998-4851-a717-2bd88dd1bfb1",
    "name": "example",
    "request": {
      "globalProfile": "global",
      "model": "GPT-5.6-sol",
      "reasoningEffort": "high",
      "stages": [
        {
          "profile": "recon"
        },
        {
          "profile": "attack"
        },
        {
          "profile": "triage"
        }
      ]
    },
    "createdAt": "2026-11-04T09:19:23.685Z",
    "updatedAt": "2026-11-04T09:19:23.685Z"
  }
]
```

To create a workflow:

```
cysu engine create-workflow --name <name>

{
  "action": "created",
  "directory": "/home/jakubjn/cysu/engine/workflows/example",
  "settings": "/home/jakubjn/cysu/engine/workflows/example/settings.yaml",
  "success": true,
  "workflow": "b6636c11-e12a-42f7-a182-d5229d13aa12"
}
```

To edit the workflow, modify the file in '~/cysu/engine/workflows/<name>/settings.yaml'. The expected format is outlined in '~/cysu/examples/example-workflow'. To save the workflow:

```
cysu engine save-workflow --name <name>

{
  "action": "updated",
  "directory": "/home/jakubjn/cysu/engine/workflows/example",
  "settings": "/home/jakubjn/cysu/engine/workflows/example/settings.yaml",
  "success": true,
  "workflow": "b6636c11-e12a-42f7-a182-d5229d13aa12"
}
```

To pull a workflow into '~/cysu/engine/workflows' for editing/inspection:

```
cysu engine get-workflow --workflow <id>

{
  "action": "downloaded",
  "directory": "/home/jakubjn/cysu/engine/workflows/recursive",
  "settings": "/home/jakubjn/cysu/engine/workflows/recursive/settings.yaml",
  "success": true,
  "workflow": "8e4759b1-9998-4851-a717-2bd88dd1bfb1"
}
```

To remove a workflow, and clear the workflow directory:

```
cysu engine delete-workflow --workflow 'b6636c11-e12a-42f7-a182-d5229d13aa12'

{
  "deleted": "b6636c11-e12a-42f7-a182-d5229d13aa12"
}

cysu engine clear-workflows
```

## Scheduling

Schedule a workflow with its own objective, a type, and an occurrence matching that type:

```
cysu engine schedule --workflow <id> --objective <prompt> --type daily --occurrence 09:00

{
  "id": "ce9d3bb7-ea84-4224-ad31-aa3964587394",
  "when": {
    "type": "daily",
    "time": "09:00"
  },
  "tz": "Europe/Dublin",
  "workspaceId": "8e4759b1-9998-4851-a717-2bd88dd1bfb1",
  "objective": "Test"
}
```

**Important** - These are the expected formats:

```
`once` -> `YYYY-MM-DDTHH:MM`
`daily` -> `HH:MM`
`weekdays` -> `HH:MM`
`weekly` -> `"mon,thu HH:MM"`
```

To remove a scheduled action:

```
cysu engine unschedule --schedule <id>

{
  "deleted": "ce9d3bb7-ea84-4224-ad31-aa3964587394"
}
```

## Runs

To run a workflow:

```
cysu engine run --workflow <id> --objective <entry-prompt>
```

To list current and previous runs:

```
cysu engine list-runs

[
  {
    "id": "263f0a07-42a4-47ae-a5b2-85104567e7ac",
    "status": "cancelled",
    "createdAt": "2026-11-09T10:41:23.509Z",
    "startedAt": "2026-11-09T10:41:23.515Z",
    "finishedAt": "2026-11-09T10:41:47.577Z",
    "name": "example",
    "globalProfile": "global",
    "model": "gpt-6-sol",
    "workspace": {
      "id": "8e4759b1-9998-4851-a717-2bd88dd1bfb1",
      "name": "example-workflow"
    },
    "stages": [
      {
        "index": 0,
        "profile": "recon",
        "tag": "",
        "status": "cancelled"
      }
    ]
  }
]
```

To get the progress for a run (completed or in progress):

```
cysu engine get-run --run <id>

{
  "directory": "/home/jakubjn/cysu/engine/runs/e1bfb412-8aa7-4e9e-8905-9ae31be624a0",
  "run": "e1bfb412-8aa7-4e9e-8905-9ae31be624a0",
  "stages": [
    {
      "events": 11,
      "index": 0,
      "path": "/home/jakubjn/cysu/engine/runs/e1bfb412-8aa7-4e9e-8905-9ae31be624a0/recon.md",
      "profile": "recon"
    }
  ],
  "status": "succeeded",
  "success": true
}
```

The event logs will be available in '~/cysu/engine/runs/e1bfb412-8aa7-4e9e-8905-9ae31be624a0/'. The expected format can be found in '~/cysu/examples/example-run/'.

To clear the run directory:

```
cysu engine clear-runs

{
  "directory": "/home/jakubjn/cysu/engine/runs",
  "success": true
}
```

To cancel a run that's currently running:

```
cysu engine cancel-run --run '263f0a07-42a4-47ae-a5b2-85104567e7ac'
```
