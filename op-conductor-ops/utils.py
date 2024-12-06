from rich import print


def print_boolean(value):
    if value is None:
        return "❓" + "\u200B"
    return ("✅" if value else "❌") + "\u200B"


def make_rpc_payload(method: str, params: list = None):
    if params is None:
        params = []
    return {
        "id": 1,
        "jsonrpc": "2.0",
        "method": method,
        "params": params,
    }


def print_error(msg: str):
    print(f"[bold red]ERROR![/bold red] {msg}")


def print_warn(msg: str):
    print(f"[bold yellow]WARNING![/bold yellow] {msg}")
