import glob
import os
import shutil
from random import shuffle
from typing import List

import click
from jinja2 import Template

from focusd import sync

from . import templates

fake_systemd_name_path: str = "/home/frank.sun/devel/focusd/data/fake_systemd_names.csv"
bin_src_folder = "/home/frank.sun/devel/focusd/install/dist/"

# for real
system_service_file_folder = "/lib/systemd/system/"
bin_dst_folder = "/usr/bin"
# for test
# system_service_file_folder = "/tmp/lib/systemd/system/"
# bin_dst_folder = "/tmp/usr/bin"


def get_fake_service_paths() -> List[str]:
    # get list of service file: folder/xxx.service
    fake_service_names: List[str] = []

    with open(fake_systemd_name_path) as f:
        fake_service_names.extend(f.readlines())

    res: List[str] = []
    for fake_name in fake_service_names:
        service_file_path: str = os.path.join(
            system_service_file_folder, fake_name.strip() + ".service"
        )
        res.append(service_file_path)

    res = list(set(res))
    res.sort()

    return res


def check_if_fake_exist() -> None:
    service_file_paths: List[str] = get_fake_service_paths()
    for service_file_path in service_file_paths:
        if os.path.exists(service_file_path):
            raise FileExistsError(
                "file {path} already exist, choose another for fake name".format(
                    path=service_file_path
                )
            )


# get current count of focusd.service system fs
def get_fake_systemd_service_from_fs() -> List[str]:
    service_files: List[str] = [
        f
        for f in glob.glob(os.path.join(system_service_file_folder, "*.service"))
        if os.path.isfile(f)
    ]
    service_files.sort()

    fake_services_path: List[str] = get_fake_service_paths()
    fake_services_path.sort()

    intersects: List[str] = list(set(fake_services_path).intersection(service_files))
    intersects.sort()

    return intersects


def get_target_system_service_paths(count: int) -> List[str]:
    target_systemd_services: List[str] = []
    # generate systemd service file path list
    fake_service_paths: List[str] = get_fake_service_paths()
    systemd_service_paths_fs: List[str] = get_fake_systemd_service_from_fs()

    target_systemd_services.extend(systemd_service_paths_fs)

    len_current: int = len(set(systemd_service_paths_fs))

    if len_current < count:
        diff: List[str] = list(set(fake_service_paths) - set(systemd_service_paths_fs))
        shuffle(diff)
        target_systemd_services.extend(diff[: count - len_current])

    target_systemd_services = list(set(target_systemd_services))
    target_systemd_services.sort()
    return target_systemd_services


@click.command(name="publish", help="publish new version to systemd")
@click.option(
    "--bin-path", default=os.path.join(bin_src_folder, "focusd"), help="src binary path"
)
@click.option("--count", default=5, help="count of systemd file replicas")
def publish(bin_path: str, count: int) -> None:
    expected_systemd_services_paths: List[str] = get_target_system_service_paths(count)
    file_name_no_ext: str = ""

    for service_path in expected_systemd_services_paths:
        file_name: str = os.path.basename(service_path)
        file_name_no_ext = file_name.split(".")[0]
        systemd_service: str = Template(templates.service_template).render(
            name=file_name_no_ext
        )
        sync(systemd_service, service_path)
        os.system("systemctl stop {file_name}".format(file_name=file_name))
        shutil.copyfile(bin_path, os.path.join(bin_dst_folder, file_name_no_ext))

    os.system("systemctl daemon-reload")
    for service_path in expected_systemd_services_paths:
        file_name_no_ext = os.path.basename(service_path)
        os.system("systemctl start {file_name}".format(file_name=file_name_no_ext))
        os.system("systemctl enable {file_name}".format(file_name=file_name_no_ext))
