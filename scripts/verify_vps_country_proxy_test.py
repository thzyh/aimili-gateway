import importlib.util
import pathlib
import unittest
import urllib.parse


SCRIPT = pathlib.Path(__file__).with_name("verify-vps-country-proxy.py")
SPEC = importlib.util.spec_from_file_location("verify_vps_country_proxy", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class VLESSDocumentTests(unittest.TestCase):
    def test_reality_client_uses_xray_26_password_field(self):
        parsed = urllib.parse.urlsplit(
            "vless://00000000-0000-4000-8000-000000000000@example.test:20000"
            "?flow=xtls-rprx-vision&fp=chrome&sni=www.microsoft.com&pbk=safe-public-value&sid=01020304"
        )
        required = {key: values[0] for key, values in urllib.parse.parse_qs(parsed.query).items()}

        document = MODULE.build_vless_document(parsed, required, 19080)

        reality = document["outbounds"][0]["streamSettings"]["realitySettings"]
        self.assertEqual(reality["password"], "safe-public-value")
        self.assertNotIn("publicKey", reality)


if __name__ == "__main__":
    unittest.main()
