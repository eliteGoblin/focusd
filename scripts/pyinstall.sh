#!/bin/zsh

# output binary file in install/dist/main
python -m PyInstaller --onefile --specpath ./install --distpath ./install/dist --workpath ./install/build --name focusd focusd/__main__.py