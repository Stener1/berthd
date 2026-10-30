import AVFoundation
import SwiftUI

/// The camera, reading one QR code. Calls `done` with its text, or nil when closed.
struct QRScannerView: View {
    var done: (String?) -> Void

    var body: some View {
        ZStack(alignment: .topTrailing) {
            Scanner(done: done).ignoresSafeArea()
            Button { done(nil) } label: {
                Image(systemName: "xmark.circle.fill").font(.system(size: 32)).symbolRenderingMode(.palette)
                    .foregroundStyle(.white, .black.opacity(0.5))
            }
            .padding()
            .accessibilityLabel("Close scanner")
        }
        .background(.black)
    }

    private struct Scanner: UIViewControllerRepresentable {
        var done: (String?) -> Void
        func makeUIViewController(context: Context) -> ScannerController { ScannerController(done: done) }
        func updateUIViewController(_ vc: ScannerController, context: Context) {}
    }

    final class ScannerController: UIViewController, AVCaptureMetadataOutputObjectsDelegate {
        private let captureSession = AVCaptureSession()
        private var preview: AVCaptureVideoPreviewLayer?
        private let done: (String?) -> Void
        private var finished = false

        init(done: @escaping (String?) -> Void) {
            self.done = done
            super.init(nibName: nil, bundle: nil)
        }
        required init?(coder: NSCoder) { fatalError() }

        override func viewDidLoad() {
            super.viewDidLoad()
            guard let device = AVCaptureDevice.default(for: .video), let input = try? AVCaptureDeviceInput(device: device),
                  captureSession.canAddInput(input) else {
                label("No camera here. Paste the pairing link instead.")
                return
            }
            captureSession.addInput(input)
            let output = AVCaptureMetadataOutput()
            guard captureSession.canAddOutput(output) else { return }
            captureSession.addOutput(output)
            output.setMetadataObjectsDelegate(self, queue: .main)
            output.metadataObjectTypes = [.qr]
            let layer = AVCaptureVideoPreviewLayer(session: captureSession)
            layer.videoGravity = .resizeAspectFill
            view.layer.addSublayer(layer)
            preview = layer
            let session = captureSession
            DispatchQueue.global(qos: .userInitiated).async { session.startRunning() }
        }

        override func viewDidLayoutSubviews() {
            super.viewDidLayoutSubviews()
            preview?.frame = view.bounds
        }

        override func viewWillDisappear(_ animated: Bool) {
            super.viewWillDisappear(animated)
            let session = captureSession
            DispatchQueue.global(qos: .userInitiated).async { if session.isRunning { session.stopRunning() } }
        }

        private func label(_ text: String) {
            let l = UILabel()
            l.text = text
            l.textColor = .white
            l.numberOfLines = 0
            l.textAlignment = .center
            l.translatesAutoresizingMaskIntoConstraints = false
            view.addSubview(l)
            NSLayoutConstraint.activate([
                l.centerYAnchor.constraint(equalTo: view.centerYAnchor),
                l.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: 24),
                l.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -24),
            ])
        }

        func metadataOutput(_ output: AVCaptureMetadataOutput, didOutput objects: [AVMetadataObject], from connection: AVCaptureConnection) {
            guard !finished, let code = objects.compactMap({ ($0 as? AVMetadataMachineReadableCodeObject)?.stringValue }).first else { return }
            finished = true
            done(code)
        }
    }
}
