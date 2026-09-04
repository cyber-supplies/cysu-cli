# Cyber Supplies CLI

You are working on Cyber Supplies CLI. This is a cli tool intended for autonomous security researchers (LLM Agents). It interacts with a cloud-based backend, where we store data and allow the agent to query it.

## Architecture

CLI -> Backend -> JavaScript Microservice (endpoints)
              `-> Authentication Microserice (accounts)
              `-> Cloud Storage (storage)
              `-> PostgreSQL DB (everything else)

## Output

The tool primarily outputs JSON to the LLM for parsing. We aim to never output plain texts or other formats, as we expect the agent to pipe the results from our tool directly into a JSON parser.

The "error" key is the top level object we use to output errors to the agent. We instruct the agent to check for the existance of this error key as a safety-check, before using the results of our tool in other commands. Errors from the CLI should be surfaced through this key. The backend will always output errors under this key. There is no need for additional parsing of error messages from the backend.

## Encryption

Certain fields from the requests and responses must be encrypted/decrypted before being processed. We aim to store as little unencrypted user data on our servers as possible. The algorithm for encryption is one way, only the user can encrypt + decrypt the data.

## Behaviour

- At times, the development of the CLI may lag behind that of the backend. This is expected. If the user instructs you to make a change that doesn't align with what the backend returns, then ASSUME that the response is correct, and code it exactly as if it was correct.

- Use line breaks where appropriate to format code. Write comments above functions explaining what they are for. Ask the user before implementing any changes that they have not explicity asked for.

- Use the config folder for storing data related to the tool. Currently, this includes the encryption key, and currently selected project.

- The binary is intended for multiple operating systems. Ensure everything is cross-platform compatible.