# E2 "Write a simple service" · `qwen2.5-coder:1.5b`

- **Date:** 2026-10-15
- **Model:** `qwen2.5-coder:1.5b` (ID `d7372fd82851`), no thinking phase
- **Machine:** MacBook Pro M4 Pro 24 GB, macOS 15.8.1, Ollama 0.40.3
- **Settings:** Ollama defaults (model's own temperature, context 4096), native `/api/chat` with streaming, one run, not cherry-picked
- **Prompts:** [lab-plan.md §3 E2](../../../docs/lab-plan.md)
- **Ran it** (`python3 notes.py`, Python 3.9.6, then the curl commands from the answer): works as written. `GET` → `[]`, `POST` → `201` `{"text": "Test note", "id": 1}`, `GET` again lists it. No input validation.

## 1. Service

**Prompt**

````text
Write a minimal REST service in Python using only the standard library
(http.server, json). Endpoints:
- GET /notes returns all notes as a JSON list
- POST /notes with body {"text": "..."} adds a note and returns it with an id
Keep the data in memory. Listen on 127.0.0.1:8000.
After the code, give curl commands that test both endpoints.
````

**Answer** — first word after 0.07 s, 468 tokens at 143.4 tok/s

Here is a simple Python REST service using the `http.server` module and `json` module:

```python
import http.server
import json
import socketserver

# Data in memory
notes = []

# Define the handler class
class MyHttpRequestHandler(http.server.SimpleHTTPRequestHandler):
    def do_GET(self):
        # GET /notes
        if self.path == '/notes':
            self.send_response(200)
            self.send_header('Content-type', 'application/json')
            self.end_headers()
            self.wfile.write(json.dumps(notes).encode())
        else:
            self.send_response(404)
            self.end_headers()

    def do_POST(self):
        # POST /notes
        if self.path == '/notes':
            content_length = int(self.headers['Content-Length'])
            body = self.rfile.read(content_length)
            note = json.loads(body)
            note['id'] = len(notes) + 1
            notes.append(note)
            self.send_response(201)
            self.send_header('Content-type', 'application/json')
            self.send_header('Location', f'/notes/{note["id"]}')
            self.end_headers()
            self.wfile.write(json.dumps(note).encode())
        else:
            self.send_response(404)
            self.end_headers()

# Create the server
with socketserver.TCPServer(('127.0.0.1', 8000), MyHttpRequestHandler) as httpd:
    print("Serving at http://127.0.0.1:8000")
    httpd.serve_forever()
```

Curl commands to test the endpoints:

1. GET /notes:
```sh
curl -X GET http://127.0.0.1:8000/notes
```

2. POST /notes:
```sh
curl -X POST -H "Content-Type: application/json" -d '{"text": "Test note"}' http://127.0.0.1:8000/notes
```

The first GET request will retrieve all notes as a JSON list. The second POST request will add a new note to the list and return it with an id.

