import 'dart:convert';
import 'package:http/http.dart' as http;

/// Typed API client — all calls tenant-scoped (X-Tenant-ID, §6).
class ApiClient {
  final String baseUrl;
  final String tenantId;
  final String? bearerToken;

  const ApiClient({required this.baseUrl, required this.tenantId, this.bearerToken});

  Map<String, String> get _headers => {
        'Content-Type': 'application/json',
        'X-Tenant-ID': tenantId,
        if (bearerToken != null) 'Authorization': 'Bearer $bearerToken',
      };

  Future<Map<String, dynamic>> post(String path, Map<String, dynamic> body) async {
    final res = await http.post(Uri.parse('$baseUrl$path'), headers: _headers, body: jsonEncode(body));
    if (res.statusCode >= 300) {
      throw ApiException(res.statusCode, res.body);
    }
    return jsonDecode(res.body) as Map<String, dynamic>;
  }

  Future<Map<String, dynamic>> get(String path) async {
    final res = await http.get(Uri.parse('$baseUrl$path'), headers: _headers);
    if (res.statusCode >= 300) {
      throw ApiException(res.statusCode, res.body);
    }
    return jsonDecode(res.body) as Map<String, dynamic>;
  }

  // --- Onboarding (FR-101..107) ---
  Future<Map<String, dynamic>> createMember(String name, String dob, String phone) =>
      post('/v1/members', {'full_name': name, 'dob': dob, 'phone': phone});

  Future<Map<String, dynamic>> verifyNIN(String personId, String nin, String name, String dob) =>
      post('/v1/kyc/nin/verify', {'person_id': personId, 'nin': nin, 'name': name, 'dob': dob});

  Future<Map<String, dynamic>> validateBVN(String personId, String bvn, String name, String dob, String phone) =>
      post('/v1/kyc/bvn/verify', {'person_id': personId, 'bvn': bvn, 'name': name, 'dob': dob, 'phone': phone});

  Future<Map<String, dynamic>> submitLiveness(String personId, String selfieRef, {String kind = 'ENROLL'}) =>
      post('/v1/kyc/liveness', {'person_id': personId, 'kind': kind, 'selfie_ref': selfieRef});

  Future<Map<String, dynamic>> openRSA(String personId, String fundId) =>
      post('/v1/members/$personId/rsa', {'fund_id': fundId, 'kyc_passed': true});

  // --- Member data (FR-204) ---
  Future<Map<String, dynamic>> statement(String rsaId, String period) =>
      get('/v1/members/$rsaId/statement?period=$period');

  // --- I Am Alive (FR-108) ---
  Future<Map<String, dynamic>> iAmAlive(String personId, String selfieRef) =>
      submitLiveness(personId, selfieRef, kind: 'IAM_ALIVE');
}

class ApiException implements Exception {
  final int status;
  final String body;
  const ApiException(this.status, this.body);
  @override
  String toString() => 'ApiException($status): $body';
}
