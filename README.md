
# Run

```
source .venv/bin/activate
export PYTHONPATH=/home/frank.sun/devel/focusd
cd /home/frank.sun/devel/focusd
python focusd/main.py
```

# Packing

```s
cd /home/frank.sun/devel/focusd
scripts/pyinstall.sh
# generate following file: ./dist/main
```

# Systemd

Put int `/lib/systemd/system/focusd.service`

