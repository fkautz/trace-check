// Example Rust source for the trace-check comment collector. The tag comment
// sits in the test's doc/line comment, above the #[test] attribute.

pub fn validate(token: &str) -> bool {
    !token.is_empty()
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Verifies: RUST-001
    #[test]
    fn accepts_a_valid_token() {
        assert!(validate("ok"));
    }

    /// Verifies: RUST-002
    #[test]
    fn rejects_an_expired_token() {
        assert!(!validate(""));
    }
}
