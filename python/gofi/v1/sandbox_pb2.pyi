from google.protobuf import struct_pb2 as _struct_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class SandboxDefinition(_message.Message):
    __slots__ = ("name", "source_code", "timeout_seconds", "memory_mib")
    NAME_FIELD_NUMBER: _ClassVar[int]
    SOURCE_CODE_FIELD_NUMBER: _ClassVar[int]
    TIMEOUT_SECONDS_FIELD_NUMBER: _ClassVar[int]
    MEMORY_MIB_FIELD_NUMBER: _ClassVar[int]
    name: str
    source_code: str
    timeout_seconds: int
    memory_mib: int
    def __init__(self, name: _Optional[str] = ..., source_code: _Optional[str] = ..., timeout_seconds: _Optional[int] = ..., memory_mib: _Optional[int] = ...) -> None: ...

class SandboxStatus(_message.Message):
    __slots__ = ("name", "phase", "error")
    NAME_FIELD_NUMBER: _ClassVar[int]
    PHASE_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    name: str
    phase: str
    error: str
    def __init__(self, name: _Optional[str] = ..., phase: _Optional[str] = ..., error: _Optional[str] = ...) -> None: ...

class ListSandboxesRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListSandboxesResponse(_message.Message):
    __slots__ = ("sandboxes", "statuses")
    SANDBOXES_FIELD_NUMBER: _ClassVar[int]
    STATUSES_FIELD_NUMBER: _ClassVar[int]
    sandboxes: _containers.RepeatedCompositeFieldContainer[SandboxDefinition]
    statuses: _containers.RepeatedCompositeFieldContainer[SandboxStatus]
    def __init__(self, sandboxes: _Optional[_Iterable[_Union[SandboxDefinition, _Mapping]]] = ..., statuses: _Optional[_Iterable[_Union[SandboxStatus, _Mapping]]] = ...) -> None: ...

class GetSandboxRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class GetSandboxResponse(_message.Message):
    __slots__ = ("sandbox", "status")
    SANDBOX_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    sandbox: SandboxDefinition
    status: SandboxStatus
    def __init__(self, sandbox: _Optional[_Union[SandboxDefinition, _Mapping]] = ..., status: _Optional[_Union[SandboxStatus, _Mapping]] = ...) -> None: ...

class ApplySandboxRequest(_message.Message):
    __slots__ = ("sandbox",)
    SANDBOX_FIELD_NUMBER: _ClassVar[int]
    sandbox: SandboxDefinition
    def __init__(self, sandbox: _Optional[_Union[SandboxDefinition, _Mapping]] = ...) -> None: ...

class ApplySandboxResponse(_message.Message):
    __slots__ = ("sandbox", "status")
    SANDBOX_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    sandbox: SandboxDefinition
    status: SandboxStatus
    def __init__(self, sandbox: _Optional[_Union[SandboxDefinition, _Mapping]] = ..., status: _Optional[_Union[SandboxStatus, _Mapping]] = ...) -> None: ...

class DeleteSandboxRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class DeleteSandboxResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListLibraryFunctionsRequest(_message.Message):
    __slots__ = ("library",)
    LIBRARY_FIELD_NUMBER: _ClassVar[int]
    library: str
    def __init__(self, library: _Optional[str] = ...) -> None: ...

class LibraryFunction(_message.Message):
    __slots__ = ("library", "name", "kind", "description")
    LIBRARY_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    library: str
    name: str
    kind: str
    description: str
    def __init__(self, library: _Optional[str] = ..., name: _Optional[str] = ..., kind: _Optional[str] = ..., description: _Optional[str] = ...) -> None: ...

class ListLibraryFunctionsResponse(_message.Message):
    __slots__ = ("functions",)
    FUNCTIONS_FIELD_NUMBER: _ClassVar[int]
    functions: _containers.RepeatedCompositeFieldContainer[LibraryFunction]
    def __init__(self, functions: _Optional[_Iterable[_Union[LibraryFunction, _Mapping]]] = ...) -> None: ...

class WorkerFunction(_message.Message):
    __slots__ = ("name", "kind", "description")
    NAME_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    name: str
    kind: str
    description: str
    def __init__(self, name: _Optional[str] = ..., kind: _Optional[str] = ..., description: _Optional[str] = ...) -> None: ...

class WorkerListFunctionsRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class WorkerListFunctionsResponse(_message.Message):
    __slots__ = ("functions",)
    FUNCTIONS_FIELD_NUMBER: _ClassVar[int]
    functions: _containers.RepeatedCompositeFieldContainer[WorkerFunction]
    def __init__(self, functions: _Optional[_Iterable[_Union[WorkerFunction, _Mapping]]] = ...) -> None: ...

class WorkerInvokeRequest(_message.Message):
    __slots__ = ("name", "kind", "raw", "fields", "meta")
    NAME_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    RAW_FIELD_NUMBER: _ClassVar[int]
    FIELDS_FIELD_NUMBER: _ClassVar[int]
    META_FIELD_NUMBER: _ClassVar[int]
    name: str
    kind: str
    raw: bytes
    fields: _struct_pb2.Struct
    meta: _struct_pb2.Struct
    def __init__(self, name: _Optional[str] = ..., kind: _Optional[str] = ..., raw: _Optional[bytes] = ..., fields: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., meta: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ...) -> None: ...

class WorkerInvokeResponse(_message.Message):
    __slots__ = ("fields", "raw", "drop")
    FIELDS_FIELD_NUMBER: _ClassVar[int]
    RAW_FIELD_NUMBER: _ClassVar[int]
    DROP_FIELD_NUMBER: _ClassVar[int]
    fields: _struct_pb2.Struct
    raw: bytes
    drop: bool
    def __init__(self, fields: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., raw: _Optional[bytes] = ..., drop: _Optional[bool] = ...) -> None: ...

class WorkerListenRequest(_message.Message):
    __slots__ = ("name",)
    NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    def __init__(self, name: _Optional[str] = ...) -> None: ...

class WorkerListenResponse(_message.Message):
    __slots__ = ("raw", "fields", "meta")
    RAW_FIELD_NUMBER: _ClassVar[int]
    FIELDS_FIELD_NUMBER: _ClassVar[int]
    META_FIELD_NUMBER: _ClassVar[int]
    raw: bytes
    fields: _struct_pb2.Struct
    meta: _struct_pb2.Struct
    def __init__(self, raw: _Optional[bytes] = ..., fields: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., meta: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ...) -> None: ...

class SourceDiagnostic(_message.Message):
    __slots__ = ("line", "column", "code", "message")
    LINE_FIELD_NUMBER: _ClassVar[int]
    COLUMN_FIELD_NUMBER: _ClassVar[int]
    CODE_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    line: int
    column: int
    code: str
    message: str
    def __init__(self, line: _Optional[int] = ..., column: _Optional[int] = ..., code: _Optional[str] = ..., message: _Optional[str] = ...) -> None: ...

class FormatSourceRequest(_message.Message):
    __slots__ = ("source",)
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    source: str
    def __init__(self, source: _Optional[str] = ...) -> None: ...

class FormatSourceResponse(_message.Message):
    __slots__ = ("source",)
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    source: str
    def __init__(self, source: _Optional[str] = ...) -> None: ...

class ValidateSourceRequest(_message.Message):
    __slots__ = ("source",)
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    source: str
    def __init__(self, source: _Optional[str] = ...) -> None: ...

class ValidateSourceResponse(_message.Message):
    __slots__ = ("valid", "diagnostics")
    VALID_FIELD_NUMBER: _ClassVar[int]
    DIAGNOSTICS_FIELD_NUMBER: _ClassVar[int]
    valid: bool
    diagnostics: _containers.RepeatedCompositeFieldContainer[SourceDiagnostic]
    def __init__(self, valid: _Optional[bool] = ..., diagnostics: _Optional[_Iterable[_Union[SourceDiagnostic, _Mapping]]] = ...) -> None: ...

class WorkerFormatSourceRequest(_message.Message):
    __slots__ = ("source",)
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    source: str
    def __init__(self, source: _Optional[str] = ...) -> None: ...

class WorkerFormatSourceResponse(_message.Message):
    __slots__ = ("source",)
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    source: str
    def __init__(self, source: _Optional[str] = ...) -> None: ...

class WorkerValidateSourceRequest(_message.Message):
    __slots__ = ("source",)
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    source: str
    def __init__(self, source: _Optional[str] = ...) -> None: ...

class WorkerValidateSourceResponse(_message.Message):
    __slots__ = ("valid", "diagnostics")
    VALID_FIELD_NUMBER: _ClassVar[int]
    DIAGNOSTICS_FIELD_NUMBER: _ClassVar[int]
    valid: bool
    diagnostics: _containers.RepeatedCompositeFieldContainer[SourceDiagnostic]
    def __init__(self, valid: _Optional[bool] = ..., diagnostics: _Optional[_Iterable[_Union[SourceDiagnostic, _Mapping]]] = ...) -> None: ...

class RunFunctionRequest(_message.Message):
    __slots__ = ("library", "name", "kind", "raw", "fields", "meta")
    LIBRARY_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    RAW_FIELD_NUMBER: _ClassVar[int]
    FIELDS_FIELD_NUMBER: _ClassVar[int]
    META_FIELD_NUMBER: _ClassVar[int]
    library: str
    name: str
    kind: str
    raw: bytes
    fields: _struct_pb2.Struct
    meta: _struct_pb2.Struct
    def __init__(self, library: _Optional[str] = ..., name: _Optional[str] = ..., kind: _Optional[str] = ..., raw: _Optional[bytes] = ..., fields: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., meta: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ...) -> None: ...

class RunFunctionResponse(_message.Message):
    __slots__ = ("fields", "raw", "drop")
    FIELDS_FIELD_NUMBER: _ClassVar[int]
    RAW_FIELD_NUMBER: _ClassVar[int]
    DROP_FIELD_NUMBER: _ClassVar[int]
    fields: _struct_pb2.Struct
    raw: bytes
    drop: bool
    def __init__(self, fields: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., raw: _Optional[bytes] = ..., drop: _Optional[bool] = ...) -> None: ...
