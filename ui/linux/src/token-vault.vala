namespace Clambhook {
    public interface TokenVault : Object {
        public abstract async string read_token() throws Error;
        public abstract async void save_token(string token) throws Error;
    }

    public class SecretTokenVault : Object, TokenVault {
        private const string SCHEMA_NAME = "com.clambhook.Clambhook.ApiToken";
        private const string ACCOUNT = "default";

        public async string read_token() throws Error {
            var attrs = attributes();
            string? value = yield Secret.password_lookupv(schema(), attrs, null);
            return value ?? "";
        }

        public async void save_token(string token) throws Error {
            var attrs = attributes();
            var trimmed = token.strip();
            if (trimmed == "") {
                yield Secret.password_clearv(schema(), attrs, null);
                return;
            }
            yield Secret.password_storev(
                schema(),
                attrs,
                Secret.COLLECTION_DEFAULT,
                "clambhook API token",
                trimmed,
                null
            );
        }

        private static Secret.Schema schema() {
            return new Secret.Schema(
                SCHEMA_NAME,
                Secret.SchemaFlags.NONE,
                "account",
                Secret.SchemaAttributeType.STRING
            );
        }

        private static HashTable<string, string> attributes() {
            var attrs = new HashTable<string, string>(str_hash, str_equal);
            attrs.insert("account", ACCOUNT);
            return attrs;
        }
    }

    public class MemoryTokenVault : Object, TokenVault {
        private string token = "";

        public async string read_token() throws Error {
            return token;
        }

        public async void save_token(string token) throws Error {
            this.token = token.strip();
        }
    }
}
