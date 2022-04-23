import os
import time
from typing import Any, Dict, List

import click
from jinja2 import Template

from . import sync_file, templates

output_folder: str = "/etc"
# output_folder: str = "/tmp/etc"

block_ip: str = "192.168.0.99"

dns_servers: List[Any] = [
    {
        "descrption": "opendns01",
        "ip": "208.67.220.222",
    },
    {
        "description": "opendns02",
        "ip": "208.67.220.220",
    },
]

plugins: List[Any] = [
    {
        "description": "BlockSite",
        "id": "eiimnmioipafcokbfikbljfdeojpcgbh",
    },
    {
        "description": "Force Safe Search",
        "id": "langadckdfefkcnjfmfnfeckafibfkji",
    },
    {
        "description": "StayFocusd",
        "id": "laankejkbhbdhmipfmgcngdelahlfoji",
    },
    {
        "description": "LeechBlock NG",
        "id": "blaaajhemilngeeffpbfkdjjoefldkok",
    },
]


kill_list: List[str] = [
    "brave",
    "firefox",
]

hosts_overwrite: Dict[str, List[str]] = {
    # forcesafesearch.google.com
    "216.239.38.120": [
        "www.google.com",
        "youtubei.googleapis.com",
        "youtube.googleapis.com",
    ]
}

black_list_path: str = "/home/frank.sun/devel/focusd/data/black.csv"


@click.command(name="run", help="run focusd")
@click.option("--daemon", "-d", is_flag=True, help="daemon mode")
def run(daemon: bool) -> None:

    resolv_conf_content: str = Template(templates.resolv_conf_template).render(
        dns_servers=dns_servers
    )
    chrome_policy: str = Template(templates.chrome_policy_template).render(
        plugin_id_list=[e["id"] for e in plugins]
    )
    black_list: List[str] = []
    with open(black_list_path) as f:
        black_list = [block_ip + "  " + line for line in list(f)]

    # add overwrite route
    for k in hosts_overwrite:
        black_list.extend(
            [
                "{ip} {dns_name}\n".format(ip=k, dns_name=dns_name)
                for dns_name in hosts_overwrite[k]
            ]
        )

    while True:
        os.system("killall " + " ".join(kill_list))
        sync_file.sync("".join(black_list), os.path.join(output_folder, "hosts"))
        sync_file.sync(resolv_conf_content, os.path.join(output_folder, "resolv.conf"))
        sync_file.sync(
            chrome_policy,
            os.path.join(
                output_folder, "opt/chrome/policies/managed/managed_policies.json"
            ),
        )
        if daemon is False:
            break
        time.sleep(2)
