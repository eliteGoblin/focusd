from os import path
from typing import List

import click

fake_systemd_name_path: str = "/home/frank.sun/devel/focusd/data/fake_systemd_names.csv"


def check_if_fake_exist() -> None:
    fake_names: List[str] = []
    with open(fake_systemd_name_path) as f:
        fake_names.extend(f.readlines())
    system_service_file_folder = "/lib/systemd/system"
    for fake_name in fake_names:
        service_file_path: str = path.join(
            system_service_file_folder, fake_name.strip() + ".service"
        )
        print(service_file_path)
        if path.exists(service_file_path):
            raise FileExistsError(
                "file {name} already exist, choose another for fake name".format(
                    name=fake_name
                )
            )


if __name__ == "__main__":
    check_if_fake_exist()
