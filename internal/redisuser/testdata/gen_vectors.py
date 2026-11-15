"""Generates vectors_data_test.go: the pickle test vectors of package redisuser.

Every value is stored the way dash stores it. pickle_dumps() in dash's
LocalListV3.py is zlib.compress(pickle.dumps(val)), and the default protocol is
4 on Python 3.8 to 3.13. A few values are also pickled with protocols 0, 1, 2, 3
and 5 so that the text and legacy opcodes of the decoder are exercised. The
user hashes are built the way refresh_app_user_data() builds them: a DataFrame,
to_dict(orient="index"), then one pickle_dumps() per field.

All names, e-mail addresses and passwords below are fake.

Usage, from the repository root:

    python internal/redisuser/testdata/gen_vectors.py internal/redisuser/vectors_data_test.go
    gofmt -w internal/redisuser/vectors_data_test.go

Without an argument the Go source goes to stdout. Needs pandas, numpy, pytz and
tzdata, which pandas depends on.
"""

import datetime as dt
import pickle
import platform
import sys
import warnings
import zlib
import zoneinfo

import numpy as np
import pandas as pd
import pytz

warnings.simplefilter("ignore", FutureWarning)  # dash concatenates with an empty frame, too


def pickle_dumps(val, protocol=None) -> bytes:
    raw = pickle.dumps(val) if protocol is None else pickle.dumps(val, protocol=protocol)
    return zlib.compress(raw)


NAIVE = dt.datetime(2026, 11, 15, 14, 30, 1, 123456)
# Its 10-byte state holds a NUL, a backslash and a newline, which protocol 0 has to escape.
ESCAPES = dt.datetime(2026, 11, 10, 10, 10, 10, 0x005C0A)
SGT = dt.timezone(dt.timedelta(hours=8))
SHARED = [1]
CYCLE = []
CYCLE.append(CYCLE)

VALUES = {
    "str_ascii": "Hello, TimeVault",
    "str_cjk": "陈小明",
    "str_emoji": "ok \U0001F642",
    "str_empty": "",
    "str_long": "0123456789" * 35,
    "true": True,
    "false": False,
    "none": None,
    "int_0": 0,
    "int_1": 1,
    "int_255": 255,
    "int_256": 256,
    "int_65535": 65535,
    "int_65536": 65536,
    "int_neg1": -1,
    "int_2p31": 2**31,
    "int_2p63": 2**63,
    "int_1e20": 10**20,
    "int_neg2p70": -(2**70),
    "int_2p3000": 2**3000,
    "float_1_5": 1.5,
    "float_nan": float("nan"),
    "float_inf": float("inf"),
    "float_123456": 123456.0,
    "datetime_naive": NAIVE,
    "datetime_escapes": ESCAPES,
    "datetime_utc": NAIVE.replace(tzinfo=dt.timezone.utc),
    "datetime_sgt": NAIVE.replace(tzinfo=SGT),
    "datetime_pytz_sgt": pytz.timezone("Asia/Singapore").localize(NAIVE),
    "datetime_pytz_utc": NAIVE.replace(tzinfo=pytz.utc),
    "datetime_zoneinfo": NAIVE.replace(tzinfo=zoneinfo.ZoneInfo("Asia/Singapore")),
    "ts_us": pd.Timestamp("2026-11-15 14:30:01.123456"),
    "ts_ns": pd.Timestamp("2026-11-15 14:30:01.123456789"),
    "ts_ms": pd.Timestamp("2026-11-15 14:30:01.123").as_unit("ms"),
    "ts_s": pd.Timestamp("2026-11-15 14:30:01").as_unit("s"),
    "ts_pre_epoch": pd.Timestamp("1969-12-31 23:59:59.5"),
    "ts_tz_sgt": pd.Timestamp("2026-11-15 14:30:01", tz="Asia/Singapore"),
    "ts_tz_utc": pd.Timestamp("2026-11-15 14:30:01", tz="UTC"),
    "ts_tz_fixed": pd.Timestamp("2026-11-15 14:30:01", tz=SGT),
    "ts_nat": pd.NaT,
    "np_bool_true": np.bool_(True),
    "np_bool_false": np.bool_(False),
    "np_int64": np.int64(123456),
    "np_int32": np.int32(-7),
    "np_uint8": np.uint8(200),
    "np_float64": np.float64(1.5),
    "np_float32": np.float32(2.5),
    "np_dt64_s": np.datetime64("2026-11-15T14:30:01"),
    "np_dt64_ns": np.datetime64("2026-11-15T14:30:01.123456789"),
    "np_dt64_nat": np.datetime64("NaT", "s"),
    "struct_mixed": [1, [2, 3], {"a": (4,), "b": None, 7: 1.5}, (), ("x",), (1, 2, 3, 4)],
    "struct_shared": [SHARED, SHARED],
    "struct_cycle": CYCLE,
    "set_small": {5, 6},
    "frozenset_small": frozenset({7}),
    "bytes_short": b"abc",
    "bytes_long": bytes(range(256)) + b"xyz",
    "bytearray_small": bytearray(b"ba"),
}

OTHER_PROTOCOLS = {
    0: ["str_ascii", "str_cjk", "str_emoji", "str_empty", "int_255", "int_2p31", "int_1e20", "true", "false",
        "none", "float_1_5", "datetime_escapes", "ts_us", "struct_mixed", "struct_shared", "set_small"],
    1: ["str_ascii", "int_255", "int_1e20", "float_1_5", "datetime_naive", "struct_mixed"],
    2: ["str_ascii", "int_255", "int_2p31", "int_1e20", "true", "none", "float_1_5", "datetime_naive", "ts_us",
        "np_int64", "struct_mixed", "set_small"],
    3: ["datetime_naive", "bytes_long"],
    5: ["str_ascii", "datetime_naive", "ts_us", "struct_mixed", "bytearray_small"],
}

USERS = [
    dict(ee_code="E1001", HashID="9f1c0001", Email="Alice.Tan@Example.test", Name="Alice Tan",
         HashPassword="fake-Secret-1", Type="user", UpdateDate=pd.Timestamp("2026-11-15 14:30:01.250"),
         phone_num="+6591234567", activated=True, is_company_email=True),
    dict(ee_code="E1002", HashID="9f1c0002", Email="bob.lee@example.test", Name="Bob Lee",
         HashPassword=None, Type="admin", UpdateDate=pd.Timestamp("2026-11-14 09:00:00"),
         phone_num=None, activated=False, is_company_email=None),
    dict(ee_code="E1003", HashID="9f1c0003", Email="chen.xm@example.test", Name="陈小明",
         HashPassword="fake-密码-3", Type="user", UpdateDate=pd.Timestamp("2026-11-13 08:15:00"),
         phone_num="+6598765432", activated=True, is_company_email=False),
]


def dash_hashes() -> dict:
    """The app_user_* hashes, built like refresh_app_user_data() in LocalListV3.py."""
    df1 = pd.DataFrame(USERS)
    df1["uid"] = df1.apply(lambda x: f"{x['ee_code'].lower()}_{x['Name'].lower()}_{x['Email'].lower()}", axis=1)
    df = pd.concat([df1, pd.DataFrame()], axis=0, ignore_index=True)
    df.sort_values(by=["UpdateDate"], axis=0, ascending=False, na_position="last", inplace=True)
    df.drop_duplicates(subset=["uid"], keep="first", inplace=True)
    df["index"] = df.apply(lambda row: row.get("uid"), axis=1)
    df = df.set_index("index")
    return {
        f"app_user_{uid}": {str(field): pickle_dumps(val) for field, val in fields.items()}
        for uid, fields in df.to_dict(orient="index").items()
    }


def describe(val, protocol=None) -> str:
    text = ascii(val) if isinstance(val, str) else repr(val)
    if len(text) > 60:
        text = text[:57] + "..."
    return text if protocol is None else f"{text} (protocol {protocol})"


def go_source() -> str:
    vectors = {name: (pickle_dumps(val), describe(val)) for name, val in VALUES.items()}
    for protocol, names in OTHER_PROTOCOLS.items():
        for name in names:
            vectors[f"p{protocol}_{name}"] = (pickle_dumps(VALUES[name], protocol), describe(VALUES[name], protocol))

    out = [
        "// Code generated by testdata/gen_vectors.py; DO NOT EDIT.",
        f"// Generated with Python {platform.python_version()}, pandas {pd.__version__}, numpy {np.__version__}.",
        "",
        "package redisuser",
        "",
        "// vectors maps a name to the hex of zlib.compress(pickle.dumps(value)), the bytes dash stores in a",
        "// Redis hash field. The comment of each entry is the Python repr of the value.",
        "var vectors = map[string]string{",
    ]
    for name in sorted(vectors):
        data, text = vectors[name]
        out.append(f'\t"{name}": "{data.hex()}", // {text}')
    out += [
        "}",
        "",
        "// dashHashes holds complete user hashes as refresh_app_user_data() writes them: the key and, per field,",
        "// the hex of the stored value.",
        "var dashHashes = map[string]map[string]string{",
    ]
    for key, fields in sorted(dash_hashes().items()):
        out.append(f'\t"{key}": {{')
        for field in sorted(fields):
            out.append(f'\t\t"{field}": "{fields[field].hex()}",')
        out.append("\t},")
    out += ["}", ""]
    return "\n".join(out)


if __name__ == "__main__":
    source = go_source()
    if len(sys.argv) > 1:
        with open(sys.argv[1], "w", encoding="utf-8", newline="\n") as f:
            f.write(source)
    else:
        sys.stdout.reconfigure(encoding="utf-8")
        sys.stdout.write(source)
