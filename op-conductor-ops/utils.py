def print_boolean(value):
    if value is None:
        return "❓"
    return "✅" if value else "❌"


def make_rpc_payload(method: str, params: list = None):
    if params is None:
        params = []
    return {
        "id": 1,
        "jsonrpc": "2.0",
        "method": method,
        "params": params,
    }
