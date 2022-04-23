
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
# generate following file: ./install/dist/focusd
```

```sh
# run focusd: sync files: /etc/hosts, resolv.conf, etc
./install/dist/focusd run
# Publish systemd files, run as daemon 
./install/dist/focusd publish
```

s92ksjshf9173lasjd81

# Leechblock

(SOPS?)