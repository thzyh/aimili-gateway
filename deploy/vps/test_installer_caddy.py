import os
import sys
import types
import unittest
from unittest.mock import MagicMock, patch

if os.name == "nt":
    sys.modules["fcntl"] = types.ModuleType("fcntl")

import installer


class InstallerCaddyTests(unittest.TestCase):
    def test_domain_certificate_readiness_does_not_require_gateway_http(self):
        raw = MagicMock()
        context = MagicMock()
        with patch.object(installer.socket, "create_connection", return_value=raw), \
             patch.object(installer.ssl, "create_default_context", return_value=context), \
             patch.object(installer, "http_ok", return_value=False) as http:
            self.assertTrue(installer.domain_tls_ready("fl.zouyunhui.cc.cd"))
            context.wrap_socket.assert_called_once_with(raw.__enter__.return_value,
                                                        server_hostname="fl.zouyunhui.cc.cd")
            http.assert_not_called()


if __name__ == "__main__":
    unittest.main()
