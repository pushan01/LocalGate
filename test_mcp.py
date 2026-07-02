import json
import requests

BASE_URL = "http://127.0.0.1:2525/mcp"

def mcp_post(session: requests.Session, payload: dict):
    resp = session.post(
        BASE_URL,
        headers={"Content-Type": "application/json"},
        data=json.dumps(payload),
        timeout=10,
    )
    resp.raise_for_status()
    # Streamable HTTP 返回一般是 JSON
    return resp.json(), resp.headers

def main():
    s = requests.Session()

    # 1) initialize
    init_req = {
        "jsonrpc": "2.0",
        "id": 1,
        "method": "initialize",
        "params": {
            "protocolVersion": "2025-11-25",
            "capabilities": {},
            "clientInfo": {"name": "py-test", "version": "0.1"},
        },
    }
    init_res, init_headers = mcp_post(s, init_req)
    print("initialize response:", json.dumps(init_res, ensure_ascii=False, indent=2))

    # Streamable HTTP 通常会下发会话头（如果是 stateful）
    session_id = init_headers.get("Mcp-Session-Id") or init_headers.get("mcp-session-id")
    if session_id:
        s.headers.update({"Mcp-Session-Id": session_id})
        print("Mcp-Session-Id:", session_id)

    # 2) tools/list
    list_req = {"jsonrpc": "2.0", "id": 2, "method": "tools/list"}
    list_res, _ = mcp_post(s, list_req)
    print("tools/list response:", json.dumps(list_res, ensure_ascii=False, indent=2))

    # 3) tools/call -> run_command (要求你的 workspace allowlist 允许 echo/sh/bash 等)
    call_req = {
        "jsonrpc": "2.0",
        "id": 3,
        "method": "tools/call",
        "params": {
            "name": "run_command",
            "arguments": {"command": "echo hello-from-mcp"},
        },
    }
    call_res, _ = mcp_post(s, call_req)
    print("tools/call response:", json.dumps(call_res, ensure_ascii=False, indent=2))

if __name__ == "__main__":
    main()
