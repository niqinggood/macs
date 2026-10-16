from __future__ import print_function, unicode_literals

import argparse
import errno
import hashlib
import json
import os
import sys
import tempfile
import time


VENDOR_LIMITS = [
    [0.0, 180.0],
    [0.0, 180.0],
    [0.0, 180.0],
    [0.0, 180.0],
    [0.0, 270.0],
    [0.0, 180.0],
]
DEFAULT_MAX_DELTA = [15.0, 15.0, 15.0, 15.0, 20.0, 35.0]
MIN_DURATION_MS = 300
MAX_DURATION_MS = 5000
VERIFY_TOLERANCE_DEG = 6.0
DEFAULT_PORT = "/dev/myserial"
MOTION_ACTIONS = ("move-joint", "move-pose", "gripper")


class DofbotError(Exception):
    def __init__(self, code, message, retry_safe=True, motion_state="not_started"):
        Exception.__init__(self, message)
        self.code = code
        self.message = message
        self.retry_safe = bool(retry_safe)
        self.motion_state = motion_state


class PortLock(object):
    def __init__(self, port):
        digest = hashlib.sha256(_bytes(port)).hexdigest()[:20]
        self.path = os.path.join(
            tempfile.gettempdir(), "openkitty-dofbot-%s.lock" % digest
        )
        self.acquired = False

    def __enter__(self):
        for _attempt in range(2):
            try:
                descriptor = os.open(
                    self.path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600
                )
                os.write(descriptor, _bytes(str(os.getpid())))
                os.close(descriptor)
                self.acquired = True
                return self
            except OSError as exc:
                if exc.errno != errno.EEXIST:
                    raise DofbotError("lock_failed", "cannot lock serial port: %s" % exc)
                owner = _read_lock_pid(self.path)
                if owner and not _process_alive(owner):
                    try:
                        os.unlink(self.path)
                        continue
                    except OSError:
                        pass
                raise DofbotError(
                    "port_busy",
                    "another Dofbot process owns the serial-port lock"
                    + (": pid %s" % owner if owner else ""),
                )
        raise DofbotError("port_busy", "serial-port lock could not be acquired")

    def __exit__(self, _exc_type, _exc_value, _traceback):
        if self.acquired:
            try:
                os.unlink(self.path)
            except OSError:
                pass
        self.acquired = False


class MockArm(object):
    def __init__(self):
        self.joints = [90.0, 90.0, 90.0, 90.0, 90.0, 90.0]
        self.calls = []

    def Arm_serial_servo_read(self, joint):
        self.calls.append(["Arm_serial_servo_read", int(joint)])
        return self.joints[int(joint) - 1]

    def Arm_serial_servo_write(self, joint, angle, duration_ms):
        self.calls.append(
            ["Arm_serial_servo_write", int(joint), float(angle), int(duration_ms)]
        )
        self.joints[int(joint) - 1] = float(angle)

    def Arm_serial_servo_write6(
        self, s1, s2, s3, s4, s5, s6, duration_ms
    ):
        values = [s1, s2, s3, s4, s5, s6]
        self.calls.append(
            ["Arm_serial_servo_write6", values, int(duration_ms)]
        )
        self.joints = [float(value) for value in values]

    def Arm_Action_Mode(self, mode):
        self.calls.append(["Arm_Action_Mode", int(mode)])


def _bytes(value):
    if isinstance(value, bytes):
        return value
    return str(value).encode("utf-8")


def _read_lock_pid(path):
    try:
        with open(path, "rb") as handle:
            raw = handle.read(40).strip()
        return int(raw)
    except (IOError, OSError, TypeError, ValueError):
        return None


def _process_alive(pid):
    if pid is None or pid <= 0:
        return False
    if os.name != "posix":
        return True
    try:
        os.kill(pid, 0)
        return True
    except OSError as exc:
        return exc.errno == errno.EPERM


def _emit(payload):
    sys.stdout.write(json.dumps(payload, ensure_ascii=True, sort_keys=True))
    sys.stdout.write("\n")
    sys.stdout.flush()


def _number(value, label):
    try:
        number = float(value)
    except (TypeError, ValueError):
        raise DofbotError("invalid_parameter", "%s must be a number" % label)
    if number != number or number in (float("inf"), float("-inf")):
        raise DofbotError("invalid_parameter", "%s must be finite" % label)
    return number


def _default_profile():
    return {
        "port": DEFAULT_PORT,
        "joint_limits_deg": [list(pair) for pair in VENDOR_LIMITS],
        "max_delta_deg": list(DEFAULT_MAX_DELTA),
        "poses": {},
    }


def _load_profile(path):
    profile = _default_profile()
    if not path:
        return profile
    try:
        with open(path, "rb") as handle:
            supplied = json.load(handle)
    except (IOError, OSError, ValueError) as exc:
        raise DofbotError("profile_error", "cannot read profile: %s" % exc)
    if not isinstance(supplied, dict):
        raise DofbotError("profile_error", "profile root must be a JSON object")

    if supplied.get("port"):
        profile["port"] = str(supplied["port"]).strip()

    if "joint_limits_deg" in supplied:
        limits = supplied["joint_limits_deg"]
        if not isinstance(limits, list) or len(limits) != 6:
            raise DofbotError(
                "profile_error", "joint_limits_deg must contain six [min,max] pairs"
            )
        normalized = []
        for index, pair in enumerate(limits):
            if not isinstance(pair, list) or len(pair) != 2:
                raise DofbotError(
                    "profile_error", "joint limit %d must be [min,max]" % (index + 1)
                )
            minimum = _number(pair[0], "joint limit minimum")
            maximum = _number(pair[1], "joint limit maximum")
            hard_minimum, hard_maximum = VENDOR_LIMITS[index]
            if (
                minimum < hard_minimum
                or maximum > hard_maximum
                or minimum >= maximum
            ):
                raise DofbotError(
                    "profile_error",
                    "joint %d limits must stay inside %.0f..%.0f"
                    % (index + 1, hard_minimum, hard_maximum),
                )
            normalized.append([minimum, maximum])
        profile["joint_limits_deg"] = normalized

    if "max_delta_deg" in supplied:
        deltas = supplied["max_delta_deg"]
        if not isinstance(deltas, list) or len(deltas) != 6:
            raise DofbotError(
                "profile_error", "max_delta_deg must contain six values"
            )
        normalized = []
        for index, value in enumerate(deltas):
            delta = _number(value, "max delta")
            if delta <= 0 or delta > DEFAULT_MAX_DELTA[index]:
                raise DofbotError(
                    "profile_error",
                    "joint %d max delta must be >0 and <=%.0f"
                    % (index + 1, DEFAULT_MAX_DELTA[index]),
                )
            normalized.append(delta)
        profile["max_delta_deg"] = normalized

    poses = supplied.get("poses", {})
    if not isinstance(poses, dict):
        raise DofbotError("profile_error", "poses must be a JSON object")
    for name, values in poses.items():
        clean_name = str(name).strip()
        if not clean_name or not isinstance(values, list) or len(values) != 6:
            raise DofbotError(
                "profile_error", "each named pose must contain six joint angles"
            )
        normalized = [_number(value, "pose angle") for value in values]
        _validate_pose(normalized, profile)
        profile["poses"][clean_name] = normalized
    return profile


def _validate_duration(duration_ms):
    if duration_ms < MIN_DURATION_MS or duration_ms > MAX_DURATION_MS:
        raise DofbotError(
            "invalid_duration",
            "duration_ms must be between %d and %d"
            % (MIN_DURATION_MS, MAX_DURATION_MS),
        )


def _validate_joint(joint):
    if joint < 1 or joint > 6:
        raise DofbotError("invalid_joint", "joint must be between 1 and 6")


def _validate_angle(joint, angle, profile):
    _validate_joint(joint)
    minimum, maximum = profile["joint_limits_deg"][joint - 1]
    if angle < minimum or angle > maximum:
        raise DofbotError(
            "angle_out_of_range",
            "joint %d angle must be between %.1f and %.1f"
            % (joint, minimum, maximum),
        )


def _validate_pose(values, profile):
    if len(values) != 6:
        raise DofbotError("invalid_pose", "a pose requires six joint angles")
    for index, value in enumerate(values):
        _validate_angle(index + 1, value, profile)


def _validate_delta(joint, current, target, profile):
    maximum = profile["max_delta_deg"][joint - 1]
    delta = abs(float(target) - float(current))
    if delta > maximum:
        raise DofbotError(
            "delta_limit",
            "joint %d delta %.1f exceeds the configured %.1f degree limit"
            % (joint, delta, maximum),
        )


def _sdk_version(module):
    direct = getattr(module, "__version__", None)
    if direct:
        return str(direct)
    try:
        import pkg_resources

        return str(pkg_resources.get_distribution("Arm_Lib").version)
    except Exception:
        return "unknown"


def _import_sdk():
    try:
        import Arm_Lib
    except Exception as exc:
        raise DofbotError(
            "sdk_unavailable",
            "cannot import Arm_Lib: %s" % exc,
        )
    if not callable(getattr(Arm_Lib, "Arm_Device", None)):
        raise DofbotError("sdk_invalid", "Arm_Lib.Arm_Device is unavailable")
    return Arm_Lib


def _open_arm(port, use_mock):
    if use_mock:
        return None, MockArm()
    module = _import_sdk()
    try:
        return module, module.Arm_Device(port)
    except Exception as exc:
        raise DofbotError(
            "open_failed", "cannot open Dofbot SDK on %s: %s" % (port, exc)
        )


def _close_arm(arm):
    serial_port = getattr(arm, "ser", None)
    close_method = getattr(serial_port, "close", None)
    if callable(close_method):
        try:
            close_method()
        except Exception:
            pass


def _read_joint(arm, joint, profile=None):
    try:
        value = arm.Arm_serial_servo_read(joint)
    except Exception as exc:
        raise DofbotError(
            "read_failed", "joint %d read failed: %s" % (joint, exc)
        )
    if value is None:
        raise DofbotError("read_failed", "joint %d returned no angle" % joint)
    angle = _number(value, "joint %d angle" % joint)
    if profile is not None:
        _validate_angle(joint, angle, profile)
    return angle


def _read_all(arm, profile=None):
    values = []
    for joint in range(1, 7):
        values.append(_read_joint(arm, joint, profile))
        time.sleep(0.01)
    return values


def _movement_receipt(action, requested, current, observed, arm, use_mock):
    verified = observed is not None
    if verified:
        if isinstance(requested, list):
            verified = all(
                abs(requested[index] - observed[index]) <= VERIFY_TOLERANCE_DEG
                for index in range(6)
            )
        else:
            verified = abs(requested - observed) <= VERIFY_TOLERANCE_DEG
    payload = {
        "ok": True,
        "action": action,
        "backend": "mock" if use_mock else "Arm_Lib",
        "physical": not use_mock,
        "requested": requested,
        "current_before": current,
        "observed_after": observed,
        "verified": bool(verified),
        "motion_state": "verified" if verified else "unknown",
        "retry_safe": False,
    }
    if not verified:
        payload["warning"] = (
            "command returned but read-back did not verify the target; "
            "do not retry automatically"
        )
    if use_mock:
        payload["sdk_calls"] = arm.calls
    return payload


def _doctor(port, use_mock):
    if use_mock:
        return {
            "ok": True,
            "action": "doctor",
            "backend": "mock",
            "physical": False,
            "python": sys.version.split()[0],
            "python_major": sys.version_info[0],
            "port": port,
            "port_exists": False,
            "sdk_version": "mock",
        }
    module = _import_sdk()
    port_exists = os.path.exists(port)
    port_readable = os.access(port, os.R_OK) if port_exists else False
    port_writable = os.access(port, os.W_OK) if port_exists else False
    return {
        "ok": bool(port_exists and port_readable and port_writable),
        "action": "doctor",
        "backend": "Arm_Lib",
        "physical": False,
        "python": sys.version.split()[0],
        "python_major": sys.version_info[0],
        "sdk_version": _sdk_version(module),
        "sdk_module": os.path.abspath(getattr(module, "__file__", "")),
        "port": port,
        "port_exists": port_exists,
        "port_readable": port_readable,
        "port_writable": port_writable,
        "error_code": (
            ""
            if port_exists and port_readable and port_writable
            else "port_unavailable"
        ),
        "note": "doctor imports the SDK but does not open the serial port",
    }


def _dry_run(args, profile, target):
    return {
        "ok": True,
        "action": args.action,
        "backend": "none",
        "dry_run": True,
        "physical": False,
        "target": target,
        "duration_ms": args.duration_ms,
        "execute_required": True,
        "note": "no SDK import and no serial-port access occurred",
        "limits": {
            "joint_limits_deg": profile["joint_limits_deg"],
            "max_delta_deg": profile["max_delta_deg"],
        },
    }


def _build_parser():
    parser = argparse.ArgumentParser(
        description="Bounded Dofbot Arm_Lib SDK bridge"
    )
    parser.add_argument("--port", default=None, help="exact serial device path")
    parser.add_argument("--profile", default=None, help="operator-owned JSON profile")
    parser.add_argument(
        "--mock", action="store_true", help="use an in-memory SDK for offline tests"
    )
    commands = parser.add_subparsers(dest="action")

    commands.add_parser("doctor", help="check interpreter, SDK, and port path")
    commands.add_parser("status", help="read all six joint angles")
    commands.add_parser("stop", help="send Arm_Action_Mode(0)")

    joint = commands.add_parser("move-joint", help="move one bounded joint")
    joint.add_argument("--joint", type=int, required=True)
    joint.add_argument("--angle", type=float, required=True)
    joint.add_argument("--duration-ms", type=int, required=True)
    joint.add_argument("--execute", action="store_true")

    pose = commands.add_parser("move-pose", help="move six joints together")
    pose_target = pose.add_mutually_exclusive_group(required=True)
    pose_target.add_argument("--joints", nargs=6, type=float)
    pose_target.add_argument("--name", help="named pose from the operator profile")
    pose.add_argument("--duration-ms", type=int, required=True)
    pose.add_argument("--execute", action="store_true")

    gripper = commands.add_parser("gripper", help="move joint 6 gripper")
    gripper.add_argument("--angle", type=float, required=True)
    gripper.add_argument("--duration-ms", type=int, required=True)
    gripper.add_argument("--execute", action="store_true")
    return parser


def _resolve_target(args, profile):
    if args.action == "move-joint":
        _validate_duration(args.duration_ms)
        _validate_angle(args.joint, args.angle, profile)
        return {"joint": args.joint, "angle": args.angle}
    if args.action == "gripper":
        _validate_duration(args.duration_ms)
        _validate_angle(6, args.angle, profile)
        return {"joint": 6, "angle": args.angle}
    if args.action == "move-pose":
        _validate_duration(args.duration_ms)
        if args.name:
            if args.name not in profile["poses"]:
                raise DofbotError(
                    "unknown_pose",
                    "named pose %r is not present in the operator profile" % args.name,
                )
            values = list(profile["poses"][args.name])
        else:
            values = [_number(value, "pose angle") for value in args.joints]
        _validate_pose(values, profile)
        return {"name": args.name or "", "joints": values}
    return None


def _run_with_arm(args, profile, port, target):
    with PortLock(port):
        module, arm = _open_arm(port, args.mock)
        try:
            if args.action == "status":
                values = _read_all(arm, profile)
                result = {
                    "ok": True,
                    "action": "status",
                    "backend": "mock" if args.mock else "Arm_Lib",
                    "physical": False,
                    "port": port,
                    "joints_deg": values,
                }
                if args.mock:
                    result["sdk_calls"] = arm.calls
                return result

            if args.action == "stop":
                try:
                    arm.Arm_Action_Mode(0)
                except Exception as exc:
                    raise DofbotError(
                        "stop_failed",
                        "SDK stop call failed: %s" % exc,
                        retry_safe=True,
                        motion_state="unknown",
                    )
                result = {
                    "ok": True,
                    "action": "stop",
                    "backend": "mock" if args.mock else "Arm_Lib",
                    "physical": not args.mock,
                    "sdk_function": "Arm_Action_Mode(0)",
                    "motion_state": "action_group_stop_sent",
                    "warning": (
                        "this is not a proven emergency stop for an in-flight "
                        "servo interpolation; use physical power cut in danger"
                    ),
                }
                if args.mock:
                    result["sdk_calls"] = arm.calls
                return result

            if args.action in ("move-joint", "gripper"):
                joint = target["joint"]
                angle = target["angle"]
                current = _read_joint(arm, joint, profile)
                _validate_delta(joint, current, angle, profile)
                try:
                    arm.Arm_serial_servo_write(
                        joint, angle, int(args.duration_ms)
                    )
                except Exception as exc:
                    raise DofbotError(
                        "write_failed",
                        "SDK movement call failed: %s" % exc,
                        retry_safe=False,
                        motion_state="unknown",
                    )
                time.sleep((args.duration_ms / 1000.0) + 0.15)
                try:
                    observed = _read_joint(arm, joint, profile)
                except DofbotError:
                    observed = None
                return _movement_receipt(
                    args.action, angle, current, observed, arm, args.mock
                )

            if args.action == "move-pose":
                values = target["joints"]
                current = _read_all(arm, profile)
                for index in range(6):
                    _validate_delta(index + 1, current[index], values[index], profile)
                try:
                    arm.Arm_serial_servo_write6(
                        values[0],
                        values[1],
                        values[2],
                        values[3],
                        values[4],
                        values[5],
                        int(args.duration_ms),
                    )
                except Exception as exc:
                    raise DofbotError(
                        "write_failed",
                        "SDK pose call failed: %s" % exc,
                        retry_safe=False,
                        motion_state="unknown",
                    )
                time.sleep((args.duration_ms / 1000.0) + 0.15)
                try:
                    observed = _read_all(arm, profile)
                except DofbotError:
                    observed = None
                return _movement_receipt(
                    "move-pose", values, current, observed, arm, args.mock
                )
            raise DofbotError("invalid_action", "unsupported action")
        finally:
            _close_arm(arm)


def main(expected_major=None):
    action = "startup"
    try:
        if expected_major is not None and sys.version_info[0] != expected_major:
            raise DofbotError(
                "wrong_interpreter",
                "this entry requires Python 2.7"
                if expected_major == 2
                else "this entry requires Python 3",
            )
        if expected_major == 2 and sys.version_info[:2] < (2, 7):
            raise DofbotError(
                "wrong_interpreter", "this entry requires Python 2.7"
            )

        parser = _build_parser()
        args = parser.parse_args()
        action = args.action or ""
        if not action:
            raise DofbotError("missing_action", "an action is required")

        profile = _load_profile(args.profile)
        port = (args.port or profile["port"] or DEFAULT_PORT).strip()
        if not port or "\n" in port or "\r" in port or "\x00" in port:
            raise DofbotError("invalid_port", "serial port path is invalid")

        if action == "doctor":
            result = _doctor(port, args.mock)
            _emit(result)
            return 0 if result["ok"] else 2

        target = _resolve_target(args, profile)
        if action in MOTION_ACTIONS and not args.execute:
            _emit(_dry_run(args, profile, target))
            return 0

        result = _run_with_arm(args, profile, port, target)
        _emit(result)
        return 0
    except DofbotError as exc:
        _emit(
            {
                "ok": False,
                "action": action,
                "error_code": exc.code,
                "error": exc.message,
                "retry_safe": exc.retry_safe,
                "motion_state": exc.motion_state,
            }
        )
        return 2
    except Exception as exc:
        _emit(
            {
                "ok": False,
                "action": action,
                "error_code": "unexpected_error",
                "error": str(exc),
                "retry_safe": False,
                "motion_state": "unknown"
                if action in MOTION_ACTIONS
                else "not_started",
            }
        )
        return 3


if __name__ == "__main__":
    sys.exit(main())
