from typing import Dict, Union

def config(configuration: Dict[str, Union[str,bool,int]]) -> None: ...
def string(mediatype: str, string: str) -> str: ...
def file(mediatype: str, input_filename: str, output_filename: str) -> None: ...
