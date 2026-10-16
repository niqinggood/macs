"""Run pytest programmatically"""
import sys
import os
os.chdir("storage/py_analysis")
sys.path.insert(0, ".")

import pytest
exit_code = pytest.main(["-v", "test_analysis.py"])
print(f"\nPytest exit code: {exit_code}")
sys.exit(exit_code)