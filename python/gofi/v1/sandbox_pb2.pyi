import datetime

from google.protobuf import struct_pb2 as _struct_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class DebugFunctionRequest(_message.Message):
    __slots__ = ("library", "source", "name", "kind", "raw", "fields", "meta", "config", "watches", "max_steps", "run_id", "timeout_seconds", "ai")
    LIBRARY_FIELD_NUMBER: _ClassVar[int]
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    RAW_FIELD_NUMBER: _ClassVar[int]
    FIELDS_FIELD_NUMBER: _ClassVar[int]
    META_FIELD_NUMBER: _ClassVar[int]
    CONFIG_FIELD_NUMBER: _ClassVar[int]
    WATCHES_FIELD_NUMBER: _ClassVar[int]
    MAX_STEPS_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    TIMEOUT_SECONDS_FIELD_NUMBER: _ClassVar[int]
    AI_FIELD_NUMBER: _ClassVar[int]
    library: str
    source: str
    name: str
    kind: str
    raw: bytes
    fields: _struct_pb2.Struct
    meta: _struct_pb2.Struct
    config: _struct_pb2.Struct
    watches: _containers.RepeatedScalarFieldContainer[str]
    max_steps: int
    run_id: str
    timeout_seconds: int
    ai: bool
    def __init__(self, library: _Optional[str] = ..., source: _Optional[str] = ..., name: _Optional[str] = ..., kind: _Optional[str] = ..., raw: _Optional[bytes] = ..., fields: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., meta: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., config: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., watches: _Optional[_Iterable[str]] = ..., max_steps: _Optional[int] = ..., run_id: _Optional[str] = ..., timeout_seconds: _Optional[int] = ..., ai: _Optional[bool] = ...) -> None: ...

class DebugRecording(_message.Message):
    __slots__ = ("steps", "result", "initial_globals", "truncated", "truncation_reason", "entry_line", "run_id")
    STEPS_FIELD_NUMBER: _ClassVar[int]
    RESULT_FIELD_NUMBER: _ClassVar[int]
    INITIAL_GLOBALS_FIELD_NUMBER: _ClassVar[int]
    TRUNCATED_FIELD_NUMBER: _ClassVar[int]
    TRUNCATION_REASON_FIELD_NUMBER: _ClassVar[int]
    ENTRY_LINE_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    steps: _containers.RepeatedCompositeFieldContainer[DebugStep]
    result: RunFunctionResponse
    initial_globals: _containers.RepeatedCompositeFieldContainer[DebugVariable]
    truncated: bool
    truncation_reason: str
    entry_line: int
    run_id: str
    def __init__(self, steps: _Optional[_Iterable[_Union[DebugStep, _Mapping]]] = ..., result: _Optional[_Union[RunFunctionResponse, _Mapping]] = ..., initial_globals: _Optional[_Iterable[_Union[DebugVariable, _Mapping]]] = ..., truncated: _Optional[bool] = ..., truncation_reason: _Optional[str] = ..., entry_line: _Optional[int] = ..., run_id: _Optional[str] = ...) -> None: ...

class DebugStep(_message.Message):
    __slots__ = ("index", "event", "line", "function", "depth", "stack", "locals", "changed_globals", "watches", "stdout_length", "return_value", "exception")
    INDEX_FIELD_NUMBER: _ClassVar[int]
    EVENT_FIELD_NUMBER: _ClassVar[int]
    LINE_FIELD_NUMBER: _ClassVar[int]
    FUNCTION_FIELD_NUMBER: _ClassVar[int]
    DEPTH_FIELD_NUMBER: _ClassVar[int]
    STACK_FIELD_NUMBER: _ClassVar[int]
    LOCALS_FIELD_NUMBER: _ClassVar[int]
    CHANGED_GLOBALS_FIELD_NUMBER: _ClassVar[int]
    WATCHES_FIELD_NUMBER: _ClassVar[int]
    STDOUT_LENGTH_FIELD_NUMBER: _ClassVar[int]
    RETURN_VALUE_FIELD_NUMBER: _ClassVar[int]
    EXCEPTION_FIELD_NUMBER: _ClassVar[int]
    index: int
    event: str
    line: int
    function: str
    depth: int
    stack: _containers.RepeatedCompositeFieldContainer[DebugFrame]
    locals: _containers.RepeatedCompositeFieldContainer[DebugVariable]
    changed_globals: _containers.RepeatedCompositeFieldContainer[DebugVariable]
    watches: _containers.RepeatedCompositeFieldContainer[DebugWatch]
    stdout_length: int
    return_value: DebugVariable
    exception: str
    def __init__(self, index: _Optional[int] = ..., event: _Optional[str] = ..., line: _Optional[int] = ..., function: _Optional[str] = ..., depth: _Optional[int] = ..., stack: _Optional[_Iterable[_Union[DebugFrame, _Mapping]]] = ..., locals: _Optional[_Iterable[_Union[DebugVariable, _Mapping]]] = ..., changed_globals: _Optional[_Iterable[_Union[DebugVariable, _Mapping]]] = ..., watches: _Optional[_Iterable[_Union[DebugWatch, _Mapping]]] = ..., stdout_length: _Optional[int] = ..., return_value: _Optional[_Union[DebugVariable, _Mapping]] = ..., exception: _Optional[str] = ...) -> None: ...

class DebugFrame(_message.Message):
    __slots__ = ("function", "line")
    FUNCTION_FIELD_NUMBER: _ClassVar[int]
    LINE_FIELD_NUMBER: _ClassVar[int]
    function: str
    line: int
    def __init__(self, function: _Optional[str] = ..., line: _Optional[int] = ...) -> None: ...

class DebugVariable(_message.Message):
    __slots__ = ("name", "type", "repr", "children", "changed", "truncated")
    NAME_FIELD_NUMBER: _ClassVar[int]
    TYPE_FIELD_NUMBER: _ClassVar[int]
    REPR_FIELD_NUMBER: _ClassVar[int]
    CHILDREN_FIELD_NUMBER: _ClassVar[int]
    CHANGED_FIELD_NUMBER: _ClassVar[int]
    TRUNCATED_FIELD_NUMBER: _ClassVar[int]
    name: str
    type: str
    repr: str
    children: _containers.RepeatedCompositeFieldContainer[DebugVariable]
    changed: bool
    truncated: bool
    def __init__(self, name: _Optional[str] = ..., type: _Optional[str] = ..., repr: _Optional[str] = ..., children: _Optional[_Iterable[_Union[DebugVariable, _Mapping]]] = ..., changed: _Optional[bool] = ..., truncated: _Optional[bool] = ...) -> None: ...

class DebugWatch(_message.Message):
    __slots__ = ("expression", "repr", "error")
    EXPRESSION_FIELD_NUMBER: _ClassVar[int]
    REPR_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    expression: str
    repr: str
    error: str
    def __init__(self, expression: _Optional[str] = ..., repr: _Optional[str] = ..., error: _Optional[str] = ...) -> None: ...

class WatchWorkerLogsRequest(_message.Message):
    __slots__ = ("library", "run_id", "tail_lines")
    LIBRARY_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    TAIL_LINES_FIELD_NUMBER: _ClassVar[int]
    library: str
    run_id: str
    tail_lines: int
    def __init__(self, library: _Optional[str] = ..., run_id: _Optional[str] = ..., tail_lines: _Optional[int] = ...) -> None: ...

class WorkerLogEntry(_message.Message):
    __slots__ = ("time", "stream", "line", "invocation_id", "function", "kind", "level")
    TIME_FIELD_NUMBER: _ClassVar[int]
    STREAM_FIELD_NUMBER: _ClassVar[int]
    LINE_FIELD_NUMBER: _ClassVar[int]
    INVOCATION_ID_FIELD_NUMBER: _ClassVar[int]
    FUNCTION_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    LEVEL_FIELD_NUMBER: _ClassVar[int]
    time: _timestamp_pb2.Timestamp
    stream: str
    line: str
    invocation_id: str
    function: str
    kind: str
    level: str
    def __init__(self, time: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., stream: _Optional[str] = ..., line: _Optional[str] = ..., invocation_id: _Optional[str] = ..., function: _Optional[str] = ..., kind: _Optional[str] = ..., level: _Optional[str] = ...) -> None: ...

class SandboxDefinition(_message.Message):
    __slots__ = ("name", "source_code", "timeout_seconds", "memory_mib", "tests", "managed_by", "egress", "ai")
    NAME_FIELD_NUMBER: _ClassVar[int]
    SOURCE_CODE_FIELD_NUMBER: _ClassVar[int]
    TIMEOUT_SECONDS_FIELD_NUMBER: _ClassVar[int]
    MEMORY_MIB_FIELD_NUMBER: _ClassVar[int]
    TESTS_FIELD_NUMBER: _ClassVar[int]
    MANAGED_BY_FIELD_NUMBER: _ClassVar[int]
    EGRESS_FIELD_NUMBER: _ClassVar[int]
    AI_FIELD_NUMBER: _ClassVar[int]
    name: str
    source_code: str
    timeout_seconds: int
    memory_mib: int
    tests: _containers.RepeatedCompositeFieldContainer[FunctionTest]
    managed_by: str
    egress: _containers.RepeatedCompositeFieldContainer[EgressRule]
    ai: bool
    def __init__(self, name: _Optional[str] = ..., source_code: _Optional[str] = ..., timeout_seconds: _Optional[int] = ..., memory_mib: _Optional[int] = ..., tests: _Optional[_Iterable[_Union[FunctionTest, _Mapping]]] = ..., managed_by: _Optional[str] = ..., egress: _Optional[_Iterable[_Union[EgressRule, _Mapping]]] = ..., ai: _Optional[bool] = ...) -> None: ...

class EgressRule(_message.Message):
    __slots__ = ("cidr", "port", "protocol", "namespace", "pod_selector", "description")
    class PodSelectorEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    CIDR_FIELD_NUMBER: _ClassVar[int]
    PORT_FIELD_NUMBER: _ClassVar[int]
    PROTOCOL_FIELD_NUMBER: _ClassVar[int]
    NAMESPACE_FIELD_NUMBER: _ClassVar[int]
    POD_SELECTOR_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    cidr: str
    port: int
    protocol: str
    namespace: str
    pod_selector: _containers.ScalarMap[str, str]
    description: str
    def __init__(self, cidr: _Optional[str] = ..., port: _Optional[int] = ..., protocol: _Optional[str] = ..., namespace: _Optional[str] = ..., pod_selector: _Optional[_Mapping[str, str]] = ..., description: _Optional[str] = ...) -> None: ...

class FunctionTest(_message.Message):
    __slots__ = ("name", "function", "kind", "raw", "fields", "meta", "config", "expect_fields", "expect_drop")
    NAME_FIELD_NUMBER: _ClassVar[int]
    FUNCTION_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    RAW_FIELD_NUMBER: _ClassVar[int]
    FIELDS_FIELD_NUMBER: _ClassVar[int]
    META_FIELD_NUMBER: _ClassVar[int]
    CONFIG_FIELD_NUMBER: _ClassVar[int]
    EXPECT_FIELDS_FIELD_NUMBER: _ClassVar[int]
    EXPECT_DROP_FIELD_NUMBER: _ClassVar[int]
    name: str
    function: str
    kind: str
    raw: bytes
    fields: _struct_pb2.Struct
    meta: _struct_pb2.Struct
    config: _struct_pb2.Struct
    expect_fields: _struct_pb2.Struct
    expect_drop: bool
    def __init__(self, name: _Optional[str] = ..., function: _Optional[str] = ..., kind: _Optional[str] = ..., raw: _Optional[bytes] = ..., fields: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., meta: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., config: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., expect_fields: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., expect_drop: _Optional[bool] = ...) -> None: ...

class SandboxStatus(_message.Message):
    __slots__ = ("name", "phase", "error", "ai_available")
    NAME_FIELD_NUMBER: _ClassVar[int]
    PHASE_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    AI_AVAILABLE_FIELD_NUMBER: _ClassVar[int]
    name: str
    phase: str
    error: str
    ai_available: bool
    def __init__(self, name: _Optional[str] = ..., phase: _Optional[str] = ..., error: _Optional[str] = ..., ai_available: _Optional[bool] = ...) -> None: ...

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
    __slots__ = ("library", "name", "kind", "description", "params", "managed_by", "every")
    LIBRARY_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    PARAMS_FIELD_NUMBER: _ClassVar[int]
    MANAGED_BY_FIELD_NUMBER: _ClassVar[int]
    EVERY_FIELD_NUMBER: _ClassVar[int]
    library: str
    name: str
    kind: str
    description: str
    params: _containers.RepeatedCompositeFieldContainer[FunctionParam]
    managed_by: str
    every: str
    def __init__(self, library: _Optional[str] = ..., name: _Optional[str] = ..., kind: _Optional[str] = ..., description: _Optional[str] = ..., params: _Optional[_Iterable[_Union[FunctionParam, _Mapping]]] = ..., managed_by: _Optional[str] = ..., every: _Optional[str] = ...) -> None: ...

class FunctionParam(_message.Message):
    __slots__ = ("name", "type", "default", "required", "description")
    NAME_FIELD_NUMBER: _ClassVar[int]
    TYPE_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_FIELD_NUMBER: _ClassVar[int]
    REQUIRED_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    name: str
    type: str
    default: _struct_pb2.Value
    required: bool
    description: str
    def __init__(self, name: _Optional[str] = ..., type: _Optional[str] = ..., default: _Optional[_Union[_struct_pb2.Value, _Mapping]] = ..., required: _Optional[bool] = ..., description: _Optional[str] = ...) -> None: ...

class ListLibraryFunctionsResponse(_message.Message):
    __slots__ = ("functions",)
    FUNCTIONS_FIELD_NUMBER: _ClassVar[int]
    functions: _containers.RepeatedCompositeFieldContainer[LibraryFunction]
    def __init__(self, functions: _Optional[_Iterable[_Union[LibraryFunction, _Mapping]]] = ...) -> None: ...

class WorkerDebugRequest(_message.Message):
    __slots__ = ("name", "kind", "raw", "fields", "meta", "config", "watches", "max_steps", "invocation_id")
    NAME_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    RAW_FIELD_NUMBER: _ClassVar[int]
    FIELDS_FIELD_NUMBER: _ClassVar[int]
    META_FIELD_NUMBER: _ClassVar[int]
    CONFIG_FIELD_NUMBER: _ClassVar[int]
    WATCHES_FIELD_NUMBER: _ClassVar[int]
    MAX_STEPS_FIELD_NUMBER: _ClassVar[int]
    INVOCATION_ID_FIELD_NUMBER: _ClassVar[int]
    name: str
    kind: str
    raw: bytes
    fields: _struct_pb2.Struct
    meta: _struct_pb2.Struct
    config: _struct_pb2.Struct
    watches: _containers.RepeatedScalarFieldContainer[str]
    max_steps: int
    invocation_id: str
    def __init__(self, name: _Optional[str] = ..., kind: _Optional[str] = ..., raw: _Optional[bytes] = ..., fields: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., meta: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., config: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., watches: _Optional[_Iterable[str]] = ..., max_steps: _Optional[int] = ..., invocation_id: _Optional[str] = ...) -> None: ...

class WorkerFunction(_message.Message):
    __slots__ = ("name", "kind", "description", "params", "every")
    NAME_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    PARAMS_FIELD_NUMBER: _ClassVar[int]
    EVERY_FIELD_NUMBER: _ClassVar[int]
    name: str
    kind: str
    description: str
    params: _containers.RepeatedCompositeFieldContainer[FunctionParam]
    every: str
    def __init__(self, name: _Optional[str] = ..., kind: _Optional[str] = ..., description: _Optional[str] = ..., params: _Optional[_Iterable[_Union[FunctionParam, _Mapping]]] = ..., every: _Optional[str] = ...) -> None: ...

class WorkerListFunctionsRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class WorkerListFunctionsResponse(_message.Message):
    __slots__ = ("functions",)
    FUNCTIONS_FIELD_NUMBER: _ClassVar[int]
    functions: _containers.RepeatedCompositeFieldContainer[WorkerFunction]
    def __init__(self, functions: _Optional[_Iterable[_Union[WorkerFunction, _Mapping]]] = ...) -> None: ...

class WorkerInvokeRequest(_message.Message):
    __slots__ = ("name", "kind", "raw", "fields", "meta", "config", "invocation_id", "trace_lines")
    NAME_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    RAW_FIELD_NUMBER: _ClassVar[int]
    FIELDS_FIELD_NUMBER: _ClassVar[int]
    META_FIELD_NUMBER: _ClassVar[int]
    CONFIG_FIELD_NUMBER: _ClassVar[int]
    INVOCATION_ID_FIELD_NUMBER: _ClassVar[int]
    TRACE_LINES_FIELD_NUMBER: _ClassVar[int]
    name: str
    kind: str
    raw: bytes
    fields: _struct_pb2.Struct
    meta: _struct_pb2.Struct
    config: _struct_pb2.Struct
    invocation_id: str
    trace_lines: bool
    def __init__(self, name: _Optional[str] = ..., kind: _Optional[str] = ..., raw: _Optional[bytes] = ..., fields: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., meta: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., config: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., invocation_id: _Optional[str] = ..., trace_lines: _Optional[bool] = ...) -> None: ...

class WorkerInvokeResponse(_message.Message):
    __slots__ = ("fields", "raw", "drop", "stdout", "stderr", "duration_us", "error", "error_location", "entry_line", "executed_lines", "invocation_id")
    FIELDS_FIELD_NUMBER: _ClassVar[int]
    RAW_FIELD_NUMBER: _ClassVar[int]
    DROP_FIELD_NUMBER: _ClassVar[int]
    STDOUT_FIELD_NUMBER: _ClassVar[int]
    STDERR_FIELD_NUMBER: _ClassVar[int]
    DURATION_US_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    ERROR_LOCATION_FIELD_NUMBER: _ClassVar[int]
    ENTRY_LINE_FIELD_NUMBER: _ClassVar[int]
    EXECUTED_LINES_FIELD_NUMBER: _ClassVar[int]
    INVOCATION_ID_FIELD_NUMBER: _ClassVar[int]
    fields: _struct_pb2.Struct
    raw: bytes
    drop: bool
    stdout: str
    stderr: str
    duration_us: int
    error: str
    error_location: SourceDiagnostic
    entry_line: int
    executed_lines: _containers.RepeatedScalarFieldContainer[int]
    invocation_id: str
    def __init__(self, fields: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., raw: _Optional[bytes] = ..., drop: _Optional[bool] = ..., stdout: _Optional[str] = ..., stderr: _Optional[str] = ..., duration_us: _Optional[int] = ..., error: _Optional[str] = ..., error_location: _Optional[_Union[SourceDiagnostic, _Mapping]] = ..., entry_line: _Optional[int] = ..., executed_lines: _Optional[_Iterable[int]] = ..., invocation_id: _Optional[str] = ...) -> None: ...

class WorkerSourceRequest(_message.Message):
    __slots__ = ("name", "config", "flow", "state", "invocation_id")
    NAME_FIELD_NUMBER: _ClassVar[int]
    CONFIG_FIELD_NUMBER: _ClassVar[int]
    FLOW_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    INVOCATION_ID_FIELD_NUMBER: _ClassVar[int]
    name: str
    config: _struct_pb2.Struct
    flow: str
    state: _struct_pb2.Struct
    invocation_id: str
    def __init__(self, name: _Optional[str] = ..., config: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., flow: _Optional[str] = ..., state: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., invocation_id: _Optional[str] = ...) -> None: ...

class SourceFrame(_message.Message):
    __slots__ = ("data", "meta")
    class MetaEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    DATA_FIELD_NUMBER: _ClassVar[int]
    META_FIELD_NUMBER: _ClassVar[int]
    data: bytes
    meta: _containers.ScalarMap[str, str]
    def __init__(self, data: _Optional[bytes] = ..., meta: _Optional[_Mapping[str, str]] = ...) -> None: ...

class WorkerFetchResponse(_message.Message):
    __slots__ = ("frames", "state", "stdout", "stderr", "duration_us", "error", "error_location", "invocation_id")
    FRAMES_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    STDOUT_FIELD_NUMBER: _ClassVar[int]
    STDERR_FIELD_NUMBER: _ClassVar[int]
    DURATION_US_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    ERROR_LOCATION_FIELD_NUMBER: _ClassVar[int]
    INVOCATION_ID_FIELD_NUMBER: _ClassVar[int]
    frames: _containers.RepeatedCompositeFieldContainer[SourceFrame]
    state: _struct_pb2.Struct
    stdout: str
    stderr: str
    duration_us: int
    error: str
    error_location: SourceDiagnostic
    invocation_id: str
    def __init__(self, frames: _Optional[_Iterable[_Union[SourceFrame, _Mapping]]] = ..., state: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., stdout: _Optional[str] = ..., stderr: _Optional[str] = ..., duration_us: _Optional[int] = ..., error: _Optional[str] = ..., error_location: _Optional[_Union[SourceDiagnostic, _Mapping]] = ..., invocation_id: _Optional[str] = ...) -> None: ...

class WorkerStreamResponse(_message.Message):
    __slots__ = ("frames", "state", "started", "invocation_id")
    FRAMES_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    STARTED_FIELD_NUMBER: _ClassVar[int]
    INVOCATION_ID_FIELD_NUMBER: _ClassVar[int]
    frames: _containers.RepeatedCompositeFieldContainer[SourceFrame]
    state: _struct_pb2.Struct
    started: bool
    invocation_id: str
    def __init__(self, frames: _Optional[_Iterable[_Union[SourceFrame, _Mapping]]] = ..., state: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., started: _Optional[bool] = ..., invocation_id: _Optional[str] = ...) -> None: ...

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
    __slots__ = ("library", "name", "kind", "raw", "fields", "meta", "config", "run_id", "trace_lines")
    LIBRARY_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    RAW_FIELD_NUMBER: _ClassVar[int]
    FIELDS_FIELD_NUMBER: _ClassVar[int]
    META_FIELD_NUMBER: _ClassVar[int]
    CONFIG_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    TRACE_LINES_FIELD_NUMBER: _ClassVar[int]
    library: str
    name: str
    kind: str
    raw: bytes
    fields: _struct_pb2.Struct
    meta: _struct_pb2.Struct
    config: _struct_pb2.Struct
    run_id: str
    trace_lines: bool
    def __init__(self, library: _Optional[str] = ..., name: _Optional[str] = ..., kind: _Optional[str] = ..., raw: _Optional[bytes] = ..., fields: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., meta: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., config: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., run_id: _Optional[str] = ..., trace_lines: _Optional[bool] = ...) -> None: ...

class RunFunctionResponse(_message.Message):
    __slots__ = ("fields", "raw", "drop", "stdout", "stderr", "duration_us", "error", "error_location", "entry_line", "executed_lines", "run_id")
    FIELDS_FIELD_NUMBER: _ClassVar[int]
    RAW_FIELD_NUMBER: _ClassVar[int]
    DROP_FIELD_NUMBER: _ClassVar[int]
    STDOUT_FIELD_NUMBER: _ClassVar[int]
    STDERR_FIELD_NUMBER: _ClassVar[int]
    DURATION_US_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    ERROR_LOCATION_FIELD_NUMBER: _ClassVar[int]
    ENTRY_LINE_FIELD_NUMBER: _ClassVar[int]
    EXECUTED_LINES_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    fields: _struct_pb2.Struct
    raw: bytes
    drop: bool
    stdout: str
    stderr: str
    duration_us: int
    error: str
    error_location: SourceDiagnostic
    entry_line: int
    executed_lines: _containers.RepeatedScalarFieldContainer[int]
    run_id: str
    def __init__(self, fields: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., raw: _Optional[bytes] = ..., drop: _Optional[bool] = ..., stdout: _Optional[str] = ..., stderr: _Optional[str] = ..., duration_us: _Optional[int] = ..., error: _Optional[str] = ..., error_location: _Optional[_Union[SourceDiagnostic, _Mapping]] = ..., entry_line: _Optional[int] = ..., executed_lines: _Optional[_Iterable[int]] = ..., run_id: _Optional[str] = ...) -> None: ...

class RunSourceRequest(_message.Message):
    __slots__ = ("source", "name", "kind", "raw", "fields", "meta", "config", "timeout_seconds", "memory_mib", "run_id", "trace_lines", "ai")
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    RAW_FIELD_NUMBER: _ClassVar[int]
    FIELDS_FIELD_NUMBER: _ClassVar[int]
    META_FIELD_NUMBER: _ClassVar[int]
    CONFIG_FIELD_NUMBER: _ClassVar[int]
    TIMEOUT_SECONDS_FIELD_NUMBER: _ClassVar[int]
    MEMORY_MIB_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    TRACE_LINES_FIELD_NUMBER: _ClassVar[int]
    AI_FIELD_NUMBER: _ClassVar[int]
    source: str
    name: str
    kind: str
    raw: bytes
    fields: _struct_pb2.Struct
    meta: _struct_pb2.Struct
    config: _struct_pb2.Struct
    timeout_seconds: int
    memory_mib: int
    run_id: str
    trace_lines: bool
    ai: bool
    def __init__(self, source: _Optional[str] = ..., name: _Optional[str] = ..., kind: _Optional[str] = ..., raw: _Optional[bytes] = ..., fields: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., meta: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., config: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., timeout_seconds: _Optional[int] = ..., memory_mib: _Optional[int] = ..., run_id: _Optional[str] = ..., trace_lines: _Optional[bool] = ..., ai: _Optional[bool] = ...) -> None: ...

class DescribePythonEnvironmentRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class PythonPackage(_message.Message):
    __slots__ = ("name", "version", "summary")
    NAME_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    SUMMARY_FIELD_NUMBER: _ClassVar[int]
    name: str
    version: str
    summary: str
    def __init__(self, name: _Optional[str] = ..., version: _Optional[str] = ..., summary: _Optional[str] = ...) -> None: ...

class PythonDecorator(_message.Message):
    __slots__ = ("name", "signature", "description")
    NAME_FIELD_NUMBER: _ClassVar[int]
    SIGNATURE_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    name: str
    signature: str
    description: str
    def __init__(self, name: _Optional[str] = ..., signature: _Optional[str] = ..., description: _Optional[str] = ...) -> None: ...

class DescribePythonEnvironmentResponse(_message.Message):
    __slots__ = ("python_version", "packages", "decorators", "network_policy", "max_timeout_seconds", "max_memory_mib", "ai_available")
    PYTHON_VERSION_FIELD_NUMBER: _ClassVar[int]
    PACKAGES_FIELD_NUMBER: _ClassVar[int]
    DECORATORS_FIELD_NUMBER: _ClassVar[int]
    NETWORK_POLICY_FIELD_NUMBER: _ClassVar[int]
    MAX_TIMEOUT_SECONDS_FIELD_NUMBER: _ClassVar[int]
    MAX_MEMORY_MIB_FIELD_NUMBER: _ClassVar[int]
    AI_AVAILABLE_FIELD_NUMBER: _ClassVar[int]
    python_version: str
    packages: _containers.RepeatedCompositeFieldContainer[PythonPackage]
    decorators: _containers.RepeatedCompositeFieldContainer[PythonDecorator]
    network_policy: str
    max_timeout_seconds: int
    max_memory_mib: int
    ai_available: bool
    def __init__(self, python_version: _Optional[str] = ..., packages: _Optional[_Iterable[_Union[PythonPackage, _Mapping]]] = ..., decorators: _Optional[_Iterable[_Union[PythonDecorator, _Mapping]]] = ..., network_policy: _Optional[str] = ..., max_timeout_seconds: _Optional[int] = ..., max_memory_mib: _Optional[int] = ..., ai_available: _Optional[bool] = ...) -> None: ...
