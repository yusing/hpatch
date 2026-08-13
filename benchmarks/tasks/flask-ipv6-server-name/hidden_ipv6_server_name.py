import ast
import os
import sys
import types
import typing as t
from pathlib import Path
from urllib.parse import urlsplit


source_path = Path("src/flask/app.py")
module = ast.parse(source_path.read_text(encoding="utf-8"), filename=str(source_path))
flask_class = next(node for node in module.body if isinstance(node, ast.ClassDef) and node.name == "Flask")
run_method = next(node for node in flask_class.body if isinstance(node, ast.FunctionDef) and node.name == "run")
harness_class = ast.ClassDef(
    name="RunHarness",
    bases=[],
    keywords=[],
    body=[run_method],
    decorator_list=[],
)
extracted = ast.fix_missing_locations(ast.Module(body=[harness_class], type_ignores=[]))

calls = []
serving = types.ModuleType("werkzeug.serving")
serving.run_simple = lambda host, port, app, **options: calls.append((host, port, options))
werkzeug = types.ModuleType("werkzeug")
werkzeug.serving = serving
sys.modules["werkzeug"] = werkzeug
sys.modules["werkzeug.serving"] = serving

cli = types.SimpleNamespace(load_dotenv=lambda: None, show_server_banner=lambda debug, name: None)
namespace = {
    "__name__": "hpatch_flask_run_test",
    "t": t,
    "os": os,
    "click": types.SimpleNamespace(secho=lambda *args, **kwargs: None),
    "cli": cli,
    "urlsplit": urlsplit,
    "get_load_dotenv": lambda enabled: False,
    "get_debug_flag": lambda: False,
    "is_running_from_reloader": lambda: False,
}
exec(compile(extracted, str(source_path), "exec"), namespace)
RunHarness = namespace["RunHarness"]


def run_with(server_name):
    calls.clear()
    app = RunHarness()
    app.config = {"SERVER_NAME": server_name}
    app.debug = False
    app.name = "benchmark"
    app._got_first_request = True
    try:
        app.run(load_dotenv=False)
    except (TypeError, ValueError) as error:
        raise AssertionError("Flask.run does not parse bracketed IPv6 SERVER_NAME") from error
    assert app._got_first_request is False
    assert len(calls) == 1
    return calls[0][:2]


assert run_with("[::1]:8080") == ("::1", 8080), "Flask.run does not parse bracketed IPv6 SERVER_NAME"
assert run_with("[2001:db8::5]") == ("2001:db8::5", 5000), "Flask.run does not parse bracketed IPv6 SERVER_NAME"
assert run_with("localhost:7000") == ("localhost", 7000)
print("Flask IPv6 SERVER_NAME behavior passed")
