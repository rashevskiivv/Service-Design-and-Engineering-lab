# E2 "Write a simple service" · `qwen2.5-coder:7b`

- **Date:** 2026-10-15
- **Model:** `qwen2.5-coder:7b` (ID `dae161e27b0e`), no thinking phase
- **Machine:** MacBook Pro M4 Pro 24 GB, macOS 15.8.1, Ollama 0.40.3
- **Settings:** Ollama defaults (model's own temperature, context 4096), native `/api/chat` with streaming, one run, not cherry-picked
- **Prompts:** [lab-plan.md §3 E2](../../../docs/lab-plan.md)
- **Ran it** (`python3 notes.py`, Python 3.9.6, then the curl commands from the answer): works, but breaks two constraints of the prompt: it listens on **all interfaces** (`TCPServer(("", PORT), …)` instead of `127.0.0.1`; we changed that line before starting it), and `GET /notes` returns `{"notes": [...]}` instead of a JSON list. Both curls work.

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

**Answer** — first word after 0.27 s, 524 tokens at 47.1 tok/s

```python
import http.server
import socketserver
import json
from urllib.parse import parse_qs

class NotesHandler(http.server.BaseHTTPRequestHandler):
    notes = []

    def do_GET(self):
        if self.path == '/notes':
            self.send_response(200)
            self.send_header('Content-type', 'application/json')
            self.end_headers()
            self.wfile.write(json.dumps({'notes': self.notes}).encode())
        else:
            self.send_error(404, 'Not Found')

    def do_POST(self):
        if self.path == '/notes':
            content_length = int(self.headers['Content-Length'])
            post_data = self.rfile.read(content_length)
            data = json.loads(post_data.decode())

            if 'text' in data:
                note_id = len(self.notes) + 1
                note = {'id': note_id, 'text': data['text']}
                self.notes.append(note)

                self.send_response(201)
                self.send_header('Content-type', 'application/json')
                self.end_headers()
                self.wfile.write(json.dumps(note).encode())
            else:
                self.send_error(400, 'Invalid request')
        else:
            self.send_error(404, 'Not Found')

if __name__ == '__main__':
    PORT = 8000
    with socketserver.TCPServer(("", PORT), NotesHandler) as httpd:
        print("Serving at port", PORT)
        httpd.serve_forever()
```

### Testing with `curl`

1. **Adding a Note**
   ```sh
   curl -X POST -H "Content-Type: application/json" -d '{"text": "Hello, world!"}' http://127.0.0.1:8000/notes
   ```

2. **Getting All Notes**
   ```sh
   curl http://127.0.0.1:8000/notes
   ```

### Expected Output

1. **Adding a Note**
   ```json
   {
       "id": 1,
       "text": "Hello, world!"
   }
   ```

2. **Getting All Notes**
   ```json
   {
       "notes": [
           {
               "id": 1,
               "text": "Hello, world!"
           }
       ]
   }
   ```

These `curl` commands will help you test the `GET` and `POST` endpoints of your minimal REST service.

