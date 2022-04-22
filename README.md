
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

Put int `/etc/systemd/system/focusd.service`

Pls refer to https://stackoverflow.com/a/41316833 for configuring systemd

# Leechblock

s92ksjshf9173lasjd81(SOPS?)