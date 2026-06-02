from dotenv import load_dotenv


def load_app_env() -> None:
    load_dotenv(dotenv_path=".env", override=False)
