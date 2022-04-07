import hashlib
import os
import shutil
import tempfile
from pathlib import Path


def sync(file_content: str, file_path: str) -> None:
    if os.path.exists(file_path):
        expected_bytes: bytes = str.encode(file_content)
        file_bytes: bytes = open(file_path, "rb").read()

        if (
            hashlib.md5(expected_bytes).hexdigest()
            == hashlib.md5(file_bytes).hexdigest()
        ):
            print(file_path + " same hash, skip")
            return

    fd, path = tempfile.mkstemp()
    with os.fdopen(fd, "w") as tmp:
        tmp.write(file_content)
        print(
            "In {folder}, writing: {file}, content: {content}\n".format(
                folder=str(Path(file_path).parent), file=file_path, content=file_content
            )
        )
        Path(file_path).parent.mkdir(parents=True, exist_ok=True)
        shutil.move(path, file_path)
        os.chmod(file_path, 0o644)
