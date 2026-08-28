## Projects

POST /api/projects [USER]

PUT /api/projects/:projectId [USER]

GET /api/projects [LLM]

GET /api/projects/:projectId [LLM]

DELETE /api/projects/:projectId [USER]

{
  "id": "example-a6h17",
  "name": "example"
}

## Findings

POST /api/projects/:projectId/findings [LLM]

PUT /api/projects/:projectId//findings/:findingId [LLM]

GET /api/projects/:projectId/findings/:findingId [LLM]

GET /api/projects/:projectId/findings [LLM]

DELETE /api//projects/:projectId/findings/:findingId [LLM]

{
  "id": 1,
  "title": "XSS",
  "severity": "HIGH",
  "description": "XSS in Example\nThere is an XSS..."
}

## Endpoints

GET /api/projects/:projectId/endpoints [LLM]

QUERY /api/projects/:projectId/endpoints/search [LLM]

{
  "endpoints": [
    {
      "path": "/api/test/:id",
      "parameters": [
        {
          "name": "testing",
          "format": "BOOLEAN",
          "optional": false
        }
      ]
    }
  ]
}

## Authentication

POST /api/projects/:projectId/accounts [USER]

GET /api/projects/:projectId/accounts [LLM]

PUT /api/projects/:projectId/accounts/:accountId [USER]

DELETE /api/projects/:projectId/accounts/:accountId [USER]

{
  "id": 1,
  "username": "jakubjn",
  "password": "12345",
  "location": "http://login.jaksec.com/"
}

GET /api/projects/:projectId/accounts/:accountId/auth [LLM]

{
  "id": 1,
  "username": "jakubjn",
  "cookies": "session=xxxxxxxx; token=xxxxxxx;",
  "csrf": null
}