
# Run

```
source .venv/bin/activate
export PYTHONPATH=/home/frank.sun/devel/focusd
cd /home/frank.sun/devel/focusd
python -m focusd run
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

# Publish a change

```s
# need to pack into bin first
sudo ./install/dist/focusd publish
# put things together, everytime changes, run
scripts/pyinstall.sh && sudo ./install/dist/focusd publish
```

Note:

*  Every time data like black.csv updated, need to re-pack and publish. 

# Leechblock

(SOPS?)