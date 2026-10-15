# E2 "Write a simple service" · `qwen3.5:9b`

- **Date:** 2026-10-15
- **Model:** `qwen3.5:9b` (ID `d63f830f28d5`), thinking off (`think: false`, same as `--think=false`)
- **Machine:** MacBook Pro M4 Pro 24 GB, macOS 15.8.1, Ollama 0.40.3
- **Settings:** Ollama defaults (model's own temperature, context 4096), native `/api/chat` with streaming, one run, not cherry-picked
- **Prompts:** [lab-plan.md §3 E2](../../../docs/lab-plan.md)
- **Ran it** (`python3 notes.py`, Python 3.9.6, then the curl commands from the answer): works as written; all 4 curls pass. Ids start at 0; the code also returns `400` for a missing `text` or invalid JSON.

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

**Answer** — first word after 0.41 s, 846 tokens at 57.2 tok/s

Here is a minimal REST service using only Python's standard library (`http.server` and `json`). It maintains state in a list within the class itself, acting as an in-memory database.

### Python Code (`server.py`)

```python
import json
from http.server import HTTPServer, BaseHTTPRequestHandler
from urllib.parse import urlparse

class NoteHandler(BaseHTTPRequestHandler):
    # In-memory storage: a simple list of dictionaries
    notes = []

    def _send_json(self, data, status=200):
        self.send_response(status)
        self.send_header('Content-type', 'application/json')
        self.end_headers()
        self.wfile.write(json.dumps(data).encode())

    def do_GET(self):
        parsed_path = urlparse(self.path)
        if parsed_path.path == '/notes':
            # Return the list of all notes
            self._send_json(self.notes)
        else:
            self.send_error(404, "Not Found")

    def do_POST(self):
        parsed_path = urlparse(self.path)
        
        if parsed_path.path != '/notes':
            self.send_error(404, "Not Found")
            return

        # Read request body
        content_length = int(self.headers.get('Content-Length', 0))
        body = self.rfile.read(content_length).decode()
        
        try:
            data = json.loads(body)
            if 'text' not in data:
                self._send_json({"error": "Missing 'text' field"}, status=400)
                return

            # Create new note with an ID
            new_note = {
                "id": len(self.notes),  # Simple integer increment as ID
                "text": data['text']
            }
            
            self.notes.append(new_note)
            self._send_json(new_note, status=201)
        except json.JSONDecodeError:
            self._send_json({"error": "Invalid JSON"}, status=400)

if __name__ == '__main__':
    server_address = ('127.0.0.1', 8000)
    httpd = HTTPServer(server_address, NoteHandler)
    print(f"Server running on http://127.0.0.1:8000")
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        httpd.shutdown()
```

### Testing with cURL

**1. Start the server:**
Save the code above as `server.py` and run it:
```bash
python server.py
```

**2. Test GET endpoint:**
Lists all notes. If you haven't added any yet, it returns an empty list `[]`.
```bash
curl http://127.0.0.1:8000/notes
```

**3. Test POST endpoint (Add a note):**
Adds the note "Hello World". Since this is in-memory and lacks persistence, you would need to restart the server for every new note if you run it as a single instance without external state management, but logically here:
```bash
curl -X POST http://127.0.0.1:8000/notes \
     -H "Content-Type: application/json" \
     -d '{"text": "Hello World"}'
```

**4. Test GET again:**
Should now return the list containing the note with `id: 0`.
```bash
curl http://127.0.0.1:8000/notes
```

**5. Add another note:**
```bash
curl -X POST http://127.0.0.1:8000/notes \
     -H "Content-Type: application/json" \
     -d '{"text": "My second note"}'
```

