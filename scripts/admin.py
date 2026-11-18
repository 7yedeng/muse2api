"""Inspect the loopback admin endpoint or explicitly request a one-time code."""
import argparse
import json
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["status", "pair-code"])
    parser.add_argument("--port", type=int, default=18689)
    args = parser.parse_args()
    if not 1024 <= args.port <= 65535:
        parser.error("invalid loopback admin port")
    path = "/admin/status" if args.command == "status" else "/admin/pair-code"
    request = urllib.request.Request(f"http://127.0.0.1:{args.port}" + path,
                                     data=None if args.command == "status" else b"{}",
                                     method="GET" if args.command == "status" else "POST",
                                     headers={"Content-Type": "application/json"})
    # Never send a local admin request through a configured external proxy.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        with opener.open(request, timeout=15) as response:
            value = json.load(response)
        if args.command == "pair-code":
            print("One-time local authorization code (do not paste into a chat or public log):")
            print(value["code"])
            print("Expires in", value["expires_in"], "seconds.")
        else:
            print(json.dumps({key: value.get(key) for key in ("ready", "persisted", "busy")}, indent=2))
        return 0
    except Exception:
        print("Local administration request failed. Check the local service and its admin port.")
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
