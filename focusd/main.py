import argparse
import os
import time
from typing import Any, List

from jinja2 import Template

from focusd import sync_file, templates

block_ip: str = "192.168.0.99"

dns_servers: List[Any] = [
    {
        "description": "opendns01",
        "ip": "208.67.220.220",
    },
    {
        "descrption": "opendns02",
        "ip": "208.67.220.222",
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


if __name__ == "__main__":

    parser = argparse.ArgumentParser()
    parser.add_argument("-d", "--daemon", help="daemon mode", action="store_true")
    args = parser.parse_args()

    resolv_conf_content: str = Template(templates.resolv_conf_template).render(
        dns_servers=dns_servers
    )
    chrome_policy: str = Template(templates.chrome_policy_template).render(
        plugins=plugins
    )
    black_list: List[str] = []
    with open("/home/frank.sun/devel/focusd/data/black.csv") as f:
        black_list = [block_ip + "  " + line for line in list(f)]

    output_folder: str = "/etc"

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
        if args.daemon is False:
            break
        time.sleep(3)
