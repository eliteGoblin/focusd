import click

from focusd import publish, run


@click.group()
def cli() -> None:
    pass


if __name__ == "__main__":
    cli.add_command(run.run)
    cli.add_command(publish.publish)

    cli()
