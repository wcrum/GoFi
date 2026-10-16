"""Initialize empty named volumes without replacing existing application state."""
import json
import os
import shutil
from pathlib import Path

for directory in (Path("/data"), Path("/received")):
    directory.mkdir(parents=True, exist_ok=True)
    os.chown(directory, 65532, 65532)
    directory.chmod(0o700)
destination = Path("/data/listeners.yaml")
created = not destination.exists()
if created:
    shutil.copyfile("/opt/testenv/listeners.yaml", destination)
    os.chown(destination, 65532, 65532)
    destination.chmod(0o600)
print(json.dumps({"level": "INFO", "msg": "test environment initialized", "created_configuration": created}))
