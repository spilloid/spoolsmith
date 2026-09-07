using System;
using System.IO;
using System.Linq;
using System.Threading;
using FlaUI.Core.AutomationElements;
using FlaUI.Core.Definitions;
using Xunit;

namespace SpoolSmithGui.Tests;

/// <summary>
/// Captures the screenshots used on the product site, from the real app rather
/// than a mockup. Opt in with SPOOLSMITH_CAPTURE_SITE_SHOTS=1; without it this
/// does nothing, so CI and ordinary runs never launch a network scan or
/// overwrite committed images.
///
/// Everything published here is deliberately neutral. Saved printers are seeded
/// under C:\Users\Public so no Windows username appears in a visible path, and
/// they are given generic names. Discovery is the one screen that shows the
/// operator's own network, because a scan of the network the PC is on is the
/// feature being demonstrated.
/// </summary>
public sealed class SiteScreenshots
{
    private const string CaptureFlag = "SPOOLSMITH_CAPTURE_SITE_SHOTS";
    private static readonly string SeedDirectory =
        Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.CommonDocuments), "SpoolSmith", "profiles");

    [StaFact]
    public void Capture_site_screenshots()
    {
        if (Environment.GetEnvironmentVariable(CaptureFlag) != "1") return;

        var liveTarget = SeedProfiles();
        CaptureDiscovery(liveTarget);
        CaptureSavedPrinterScreens(liveTarget);
    }

    /// <summary>Find a printer, after the launch scan of this PC's own network.</summary>
    private void CaptureDiscovery(string? liveTarget)
    {
        using var fixture = new AppFixture(autoScan: true);
        var images = ImageDirectory(fixture);

        // Point at the neutral folder before anything suggests a save path:
        // the default is beside the executable, which puts this machine's own
        // user profile directory on screen.
        UseSeedDirectory(fixture);
        fixture.SelectTab("Find a printer");

        var output = Text(fixture, "discover-output");
        // The scan runs on launch; wait for it to report a finished sweep.
        WaitUntil(() => output.Text.Contains("after checking"), "The launch scan did not finish.", 180_000);

        // The detected network is kept on screen, but the published capture
        // rescans a narrow range around this PC's own printer: a full sweep of
        // a real network lists whatever else answers on it, and neither the
        // operator's other hosts nor their names belong on a public page.
        var narrowed = NarrowRange(liveTarget);
        if (narrowed != null)
        {
            SetText(fixture, "discover-cidr", narrowed);
            Button(fixture, "Scan").Invoke();
            // "after checking" only appears once the sweep is done; the in-progress
            // message names the same range, so matching the range alone races it.
            WaitUntil(() => output.Text.Contains("after checking") && output.Text.Contains(narrowed),
                "The narrowed scan did not finish.", 120_000);
        }

        var results = Element(fixture, "discover-results").AsListBox();
        // A real click, not SelectionItemPattern: walk reacts to the native
        // selection notification, which LB_SETCURSEL does not raise.
        if (results.Items.Length > 0) results.Items[0].Click();
        Thread.Sleep(300);
        fixture.MainWindow.CaptureToFile(Path.Combine(images, "gui-find-a-printer.png"));

        // Choosing different settings opens the setup screen with the address,
        // a name from the reported model, and a suggested driver already filled.
        if (results.Items.Length > 0)
        {
            Button(fixture, "Use different settings").Invoke();
            WaitUntil(() => !Element(fixture, "capture-target").IsOffscreen, "Add printer did not open.");
            WaitUntil(() => !string.IsNullOrEmpty(Text(fixture, "capture-driver").Text), "Drivers did not load.", 60_000);
            Thread.Sleep(300);
            fixture.MainWindow.CaptureToFile(Path.Combine(images, "gui-add-printer.png"));
        }
    }

    /// <summary>Saved printers and the reviewed plan, from seeded neutral data.</summary>
    private void CaptureSavedPrinterScreens(string? liveTarget)
    {
        using var fixture = new AppFixture();
        var images = ImageDirectory(fixture);

        UseSeedDirectory(fixture);
        var saved = Element(fixture, "profiles-list").AsListBox();
        WaitUntil(() => saved.Items.Length > 0, "The seeded printers did not appear.");
        var office = saved.Items.FirstOrDefault(item => item.Text.Contains("Office Printer"))
            ?? throw new InvalidOperationException("The seeded Office Printer was not listed.");
        office.Click();
        WaitUntil(() => Text(fixture, "profiles-output").Text.Contains("Office Printer"), "Printer details did not load.");
        Thread.Sleep(300);
        fixture.MainWindow.CaptureToFile(Path.Combine(images, "gui-saved-printers.png"));

        // Setting up a saved printer previews it straight away, but preflight
        // needs administrator rights: run unelevated it reports that instead of
        // a plan, which is not what this screen is for showing. Preview only
        // when the app says it can produce one; otherwise capture the screen as
        // it looks before previewing, which still shows the gate.
        if (IsElevated(fixture))
        {
            Button(fixture, "Set up printer").Invoke();
            // The plan pane holds progress text while the preview runs; Full
            // plan / JSON only becomes available once an outcome exists.
            WaitUntil(() => SafeEnabled(fixture, "Full plan / JSON"), "The preview did not finish.", 90_000);
        }
        else
        {
            fixture.SelectTab("Review and apply");
            SetText(fixture, "mutate-target", Path.Combine(SeedDirectory, "office-printer.json"));
        }
        Thread.Sleep(400);
        fixture.MainWindow.CaptureToFile(Path.Combine(images, "gui-review-and-apply.png"));
        Assert.True(liveTarget == null || Text(fixture, "mutate-output").Text.Length > 0);
    }

    /// <summary>
    /// Writes two saved printers to a path with no username in it. The first
    /// reuses the evidence captured from this PC's real printer when one is
    /// available, so the preview produces a genuine plan, but is renamed.
    /// </summary>
    private static string? SeedProfiles()
    {
        Directory.CreateDirectory(SeedDirectory);
        foreach (var stale in Directory.GetFiles(SeedDirectory, "*.json")) File.Delete(stale);

        string? target = null;
        var repoProfiles = Path.Combine(FindRepoRootFromHere(), "profiles");
        if (Directory.Exists(repoProfiles))
        {
            foreach (var candidate in Directory.GetFiles(repoProfiles, "*.json"))
            {
                var json = File.ReadAllText(candidate);
                using var document = System.Text.Json.JsonDocument.Parse(json);
                if (!document.RootElement.TryGetProperty("evidence", out var evidence)) continue;
                if (!document.RootElement.TryGetProperty("target", out var ip)) continue;
                if (!document.RootElement.TryGetProperty("driver_name", out var driver)) continue;
                target = ip.GetString();
                // Only the captured evidence carries over; the name is ours, and
                // any local driver package is dropped so nothing depends on a
                // path outside this folder.
                File.WriteAllText(Path.Combine(SeedDirectory, "office-printer.json"), $$"""
                    {
                      "version": 1,
                      "target": {{System.Text.Json.JsonSerializer.Serialize(target)}},
                      "printer_name": "Office Printer",
                      "driver_name": {{System.Text.Json.JsonSerializer.Serialize(driver.GetString())}},
                      "evidence": {{evidence.GetRawText()}}
                    }
                    """);
                break;
            }
        }
        if (target == null)
        {
            File.WriteAllText(Path.Combine(SeedDirectory, "office-printer.json"), SampleProfile("Office Printer", "192.168.1.50"));
        }
        File.WriteAllText(Path.Combine(SeedDirectory, "front-desk-printer.json"), SampleProfile("Front Desk Printer", "192.168.1.51"));
        return target;
    }

    private static string SampleProfile(string name, string ip) => $$"""
        {
          "version": 1,
          "target": "{{ip}}",
          "printer_name": "{{name}}",
          "driver_name": "Generic / Text Only",
          "evidence": {
            "ip": "{{ip}}",
            "http_title": "{{name}}",
            "provenance": "captured"
          }
        }
        """;

    /// <summary>Points the app at the neutral seeded saved-printer folder.</summary>
    private static void UseSeedDirectory(AppFixture fixture)
    {
        fixture.SelectTab("Saved printers");
        SetText(fixture, "profiles-dir", SeedDirectory);
        Button(fixture, "Refresh").Invoke();
        Thread.Sleep(200);
    }

    /// <summary>The eight-address block containing this PC's saved printer.</summary>
    private static string? NarrowRange(string? target)
    {
        if (target == null || !System.Net.IPAddress.TryParse(target, out var address)) return null;
        var octets = address.GetAddressBytes();
        if (octets.Length != 4) return null;
        octets[3] &= 0xF8;
        return $"{octets[0]}.{octets[1]}.{octets[2]}.{octets[3]}/29";
    }

    /// <summary>The address of a printer this PC has already captured, if any.</summary>
    private static string? LiveProfileTarget()
    {
        var repoProfiles = Path.Combine(FindRepoRootFromHere(), "profiles");
        if (!Directory.Exists(repoProfiles)) return null;
        foreach (var candidate in Directory.GetFiles(repoProfiles, "*.json"))
        {
            using var document = System.Text.Json.JsonDocument.Parse(File.ReadAllText(candidate));
            if (document.RootElement.TryGetProperty("target", out var ip)) return ip.GetString();
        }
        return null;
    }

    private static string ImageDirectory(AppFixture fixture)
    {
        var images = Path.Combine(fixture.RepoRoot, "docs", "img");
        Directory.CreateDirectory(images);
        return images;
    }

    private static string FindRepoRootFromHere()
    {
        var dir = new DirectoryInfo(AppContext.BaseDirectory);
        while (dir != null && !File.Exists(Path.Combine(dir.FullName, "go.mod"))) dir = dir.Parent;
        return dir?.FullName ?? throw new InvalidOperationException("Could not locate the repo root.");
    }

    private static AutomationElement Element(AppFixture fixture, string name) =>
        fixture.MainWindow.FindFirstDescendant(cf => cf.ByName(name))
            ?? throw new InvalidOperationException($"No control named \"{name}\".");

    private static TextBox Text(AppFixture fixture, string name) => Element(fixture, name).AsTextBox();

    private static void SetText(AppFixture fixture, string name, string value) =>
        Text(fixture, name).Patterns.Value.Pattern.SetValue(value);

    private static Button Button(AppFixture fixture, string caption)
    {
        var deadline = DateTime.UtcNow.AddSeconds(5);
        while (true)
        {
            var found = fixture.MainWindow
                .FindAllDescendants(cf => cf.ByControlType(ControlType.Button).And(cf.ByName(caption)))
                .FirstOrDefault(button => !button.IsOffscreen);
            if (found != null) return found.AsButton();
            if (DateTime.UtcNow >= deadline) throw new InvalidOperationException($"No button captioned \"{caption}\".");
            Thread.Sleep(100);
        }
    }

    /// <summary>
    /// The app states its own access level in the status line under the tabs,
    /// which is more reliable here than asking the test process about itself:
    /// what matters is what the launched app can actually do.
    /// </summary>
    private static bool IsElevated(AppFixture fixture) =>
        fixture.MainWindow.FindAllDescendants(cf => cf.ByControlType(ControlType.Text))
            .Any(text => (text.Properties.Name.ValueOrDefault ?? "").StartsWith("Administrator mode", StringComparison.Ordinal));

    /// <summary>True once a button with this caption exists and is enabled.</summary>
    private static bool SafeEnabled(AppFixture fixture, string caption)
    {
        var found = fixture.MainWindow
            .FindAllDescendants(cf => cf.ByControlType(ControlType.Button).And(cf.ByName(caption)))
            .FirstOrDefault(button => !button.IsOffscreen);
        return found != null && found.IsEnabled;
    }

    private static void WaitUntil(Func<bool> ready, string failure, int timeoutMs = 15_000)
    {
        var deadline = DateTime.UtcNow.AddMilliseconds(timeoutMs);
        while (DateTime.UtcNow < deadline)
        {
            if (ready()) return;
            Thread.Sleep(200);
        }
        throw new TimeoutException(failure);
    }
}
