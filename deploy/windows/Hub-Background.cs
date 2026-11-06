using System;
using System.Diagnostics;
using System.IO;
using System.ComponentModel;
using System.Runtime.InteropServices;

// Built as a Windows application so Task Scheduler never creates a terminal.
internal static class HubBackground
{
    private static readonly object LogLock = new object();
    private static IntPtr lifetimeJob;

    private static int Main(string[] args)
    {
        string directory = args.Length == 1 ? Path.GetFullPath(args[0]) : AppDomain.CurrentDomain.BaseDirectory;
        string launcherLog = Path.Combine(directory, "background-launcher.log");
        try
        {
            // Children inherit this job. If Task Scheduler terminates the GUI
            // launcher, closing its last job handle also stops the watchdog
            // and backend rather than leaving an orphan holding the mutex.
            ConfigureLifetimeJob();
            string script = Path.Combine(directory, "Run-Hub.ps1");
            if (!File.Exists(script)) throw new FileNotFoundException("Hub watchdog script is missing.", script);
            string powershell = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.System),
                @"WindowsPowerShell\v1.0\powershell.exe");
            var start = new ProcessStartInfo(powershell);
            start.Arguments = "-NoProfile -NonInteractive -ExecutionPolicy Bypass -File \"" + script
                + "\" -InstallDirectory \"" + directory + "\"";
            start.WorkingDirectory = directory;
            start.UseShellExecute = false;
            start.CreateNoWindow = true;
            start.RedirectStandardOutput = true;
            start.RedirectStandardError = true;
            using (var child = new Process())
            {
                child.StartInfo = start;
                child.OutputDataReceived += delegate(object sender, DataReceivedEventArgs e) { Log(launcherLog, e.Data); };
                child.ErrorDataReceived += delegate(object sender, DataReceivedEventArgs e) { Log(launcherLog, e.Data); };
                child.Start();
                child.BeginOutputReadLine();
                child.BeginErrorReadLine();
                child.WaitForExit();
                return child.ExitCode;
            }
        }
        catch (Exception error)
        {
            Log(launcherLog, error.ToString());
            return 1;
        }
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct BasicLimits
    {
        public long ProcessTime, JobTime;
        public uint Flags;
        public UIntPtr MinimumWorkingSet, MaximumWorkingSet;
        public uint ActiveProcesses;
        public UIntPtr Affinity;
        public uint Priority, SchedulingClass;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct IoCounters
    {
        public ulong ReadOperations, WriteOperations, OtherOperations;
        public ulong ReadBytes, WriteBytes, OtherBytes;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct ExtendedLimits
    {
        public BasicLimits Basic;
        public IoCounters Io;
        public UIntPtr ProcessMemory, JobMemory, PeakProcessMemory, PeakJobMemory;
    }

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr CreateJobObject(IntPtr attributes, string name);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool SetInformationJobObject(IntPtr job, int informationClass,
        ref ExtendedLimits information, uint length);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);

    private static void ConfigureLifetimeJob()
    {
        lifetimeJob = CreateJobObject(IntPtr.Zero, null);
        if (lifetimeJob == IntPtr.Zero) throw new Win32Exception();
        var limits = new ExtendedLimits();
        limits.Basic.Flags = 0x2000; // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
        if (!SetInformationJobObject(lifetimeJob, 9, ref limits,
            (uint)Marshal.SizeOf(typeof(ExtendedLimits)))) throw new Win32Exception();
        using (var self = Process.GetCurrentProcess())
        {
            if (!AssignProcessToJobObject(lifetimeJob, self.Handle)) throw new Win32Exception();
        }
        // Keep the non-inheritable handle until process exit. Closing it early
        // would terminate this launcher as well as its child processes.
    }

    private static void Log(string path, string message)
    {
        if (message == null) return;
        lock (LogLock)
        {
            File.AppendAllText(path, DateTime.Now.ToString("o") + " " + message + Environment.NewLine);
        }
    }
}
