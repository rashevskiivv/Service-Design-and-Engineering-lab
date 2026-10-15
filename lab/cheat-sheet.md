# Cheat sheet: a local coding LLM with Ollama

Replace `qwen2.5-coder:7b` with your tag from the [hardware→model table](../docs/lab-setup.md#1-pick-your-model).

## Ollama

| Do | Command |
|---|---|
| Download a model | `ollama pull qwen2.5-coder:7b` |
| List downloaded models | `ollama list` |
| One question | `ollama run qwen2.5-coder:7b "Explain what a Python decorator is"` |
| Show the speed | add `--verbose` → `eval rate` = tokens per second |
| Chat | `ollama run qwen2.5-coder:7b`, leave with `/bye` |
| Paste several lines in the chat | start and end the message with `"""` |
| Send a file (macOS / Linux) | `ollama run qwen2.5-coder:7b "Review this code: $(cat words.py)"` |
| What's loaded, GPU or CPU | `ollama ps` |
| Unload a model | `ollama stop qwen2.5-coder:7b` |
| Delete a model | `ollama rm qwen2.5-coder:7b` |
| Thinking models: answer directly | `--think=false` (`qwen3.5`), `--think=low` (`gpt-oss`) |
| More context for long code | Ollama app settings → context length 8192, or `OLLAMA_CONTEXT_LENGTH=8192 ollama serve` |

## The API (OpenAI-compatible)

Base URL `http://localhost:11434/v1`; any API key works.

```sh
curl -N http://localhost:11434/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen2.5-coder:7b","stream":true,
       "messages":[{"role":"user","content":"Reverse a string in Go"}]}'
```

```python
# pip install openai
import sys
from openai import OpenAI

client = OpenAI(base_url="http://localhost:11434/v1", api_key="ollama")
code = open(sys.argv[1]).read()
stream = client.chat.completions.create(
    model="qwen2.5-coder:7b",
    stream=True,
    # reasoning_effort="none",   # only for thinking models such as qwen3.5
    messages=[
        {"role": "system", "content": "You are a careful code reviewer. Be brief."},
        {"role": "user", "content": f"Review this code for bugs:\n\n{code}"},
    ],
)
for chunk in stream:
    if chunk.choices and chunk.choices[0].delta.content:
        print(chunk.choices[0].delta.content, end="", flush=True)
print()
```

Save it as `review.py` and run `python3 review.py words.py`.

## Prompts that work

- Say the language and what you know: "I know Python but not Go. Explain…"
- Paste the **exact** error output next to the code.
- Ask for a specific shape: "2 sentences, then only the fixed function", "a list ordered by severity".
- Give constraints: "standard library only", "listen on 127.0.0.1:8000".
- Ask for a way to check the answer: "…and give curl commands to test it".
- Then **run it**. The model can be confidently wrong.

## Don't

- Don't set `OLLAMA_HOST=0.0.0.0` on campus Wi-Fi: it shares your model with the whole network, without a password.
- Don't paste secrets (passwords, API keys) into prompts, local or not.
